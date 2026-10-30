package core

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

// The cron matcher decides whether a backup runs tonight, on both agents.
// A wrong "true" runs a heavy job at the wrong hour; a wrong "false" means
// a server silently goes unprotected, which is the failure that matters.
func TestCronMatches(t *testing.T) {
	cases := []struct {
		name string
		expr string
		when string
		want bool
	}{
		{"diário às 3h, na hora", "0 3 * * *", "2026-10-30 03:00", true},
		{"diário às 3h, um minuto depois", "0 3 * * *", "2026-10-30 03:01", false},
		{"diário às 3h, hora errada", "0 3 * * *", "2026-10-30 04:00", false},
		{"toda hora cheia", "0 * * * *", "2026-10-30 17:00", true},
		{"a cada 15 min, no 30", "*/15 * * * *", "2026-10-30 10:30", true},
		{"a cada 15 min, no 31", "*/15 * * * *", "2026-10-30 10:31", false},
		{"lista de horas, 8h", "0 8,12,18 * * *", "2026-10-30 08:00", true},
		{"lista de horas, 12h", "0 8,12,18 * * *", "2026-10-30 12:00", true},
		{"lista de horas, 9h", "0 8,12,18 * * *", "2026-10-30 09:00", false},
		{"faixa seg-sex, sexta", "0 2 * * 1-5", "2026-10-30 02:00", true},   // 30/10/2026 = sexta
		{"faixa seg-sex, sábado", "0 2 * * 1-5", "2026-10-31 02:00", false}, // sábado
		{"domingo (0), domingo", "0 4 * * 0", "2026-11-01 04:00", true},
		{"dia do mês", "0 1 15 * *", "2026-10-15 01:00", true},
		{"dia do mês, outro dia", "0 1 15 * *", "2026-10-16 01:00", false},
		{"mês específico", "0 1 1 10 *", "2026-10-01 01:00", true},
		{"mês específico, outro mês", "0 1 1 10 *", "2026-11-01 01:00", false},
		{"todo minuto", "* * * * *", "2026-10-30 13:47", true},

		// A malformed expression must never match: a typo should mean
		// "this job never ran, go look at it", never "this job runs 1440
		// times a day".
		{"campos de menos", "0 3 * *", "2026-10-30 03:00", false},
		{"campos de mais", "0 3 * * * *", "2026-10-30 03:00", false},
		{"texto", "todo dia", "2026-10-30 03:00", false},
		{"vazio", "", "2026-10-30 03:00", false},
		{"nome de dia", "0 3 * * MON", "2026-10-26 03:00", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CronMatches(c.expr, at(c.when)); got != c.want {
				t.Errorf("CronMatches(%q, %s) = %v, queria %v", c.expr, c.when, got, c.want)
			}
		})
	}
}

// Seconds must be ignored: the scheduler ticks several times a minute, and
// a matcher that only matched at :00 seconds would skip jobs entirely.
func TestCronMatchesIgnoresSeconds(t *testing.T) {
	base := at("2026-10-30 03:00")
	for _, sec := range []int{0, 7, 31, 59} {
		when := base.Add(time.Duration(sec) * time.Second)
		if !CronMatches("0 3 * * *", when) {
			t.Errorf("deveria casar com %d segundos", sec)
		}
	}
}
