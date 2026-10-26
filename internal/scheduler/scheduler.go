// Package scheduler is a minimal standard 5-field cron matcher ("minute
// hour day month weekday", e.g. "0 3 * * *" = every day at 3am) plus the
// agent's background loop that fires due jobs via jobexec.Engine.
//
// This intentionally doesn't aim for full cron spec coverage (no named
// months/weekdays, no "L"/"W"/"#" extensions) — numbers, "*", comma lists,
// ranges ("a-b") and steps ("*/N", "a-b/N") cover the vast majority of real
// schedules without pulling in an external dependency.
package scheduler

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/jobexec"
)

// Matches reports whether cron expression expr is due at time t (minute
// granularity — seconds are ignored). Returns false for a malformed
// expression rather than erroring, so one bad job can't crash the loop.
func Matches(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}
	return matchField(fields[0], t.Minute(), 0, 59) &&
		matchField(fields[1], t.Hour(), 0, 23) &&
		matchField(fields[2], t.Day(), 1, 31) &&
		matchField(fields[3], int(t.Month()), 1, 12) &&
		matchField(fields[4], int(t.Weekday()), 0, 6)
}

func matchField(f string, val, min, max int) bool {
	for _, part := range strings.Split(f, ",") {
		if matchPart(part, val, min, max) {
			return true
		}
	}
	return false
}

func matchPart(part string, val, min, max int) bool {
	base, step := part, 0
	if i := strings.IndexByte(part, '/'); i >= 0 {
		base = part[:i]
		step, _ = strconv.Atoi(part[i+1:])
	}

	lo, hi := min, max
	switch {
	case base == "*":
		// lo/hi already full range
	case strings.Contains(base, "-"):
		segs := strings.SplitN(base, "-", 2)
		a, errA := strconv.Atoi(segs[0])
		b, errB := strconv.Atoi(segs[1])
		if errA != nil || errB != nil {
			return false
		}
		lo, hi = a, b
	default:
		n, err := strconv.Atoi(base)
		if err != nil {
			return false
		}
		if step <= 0 {
			return n == val
		}
		lo, hi = n, max
	}

	if val < lo || val > hi {
		return false
	}
	if step <= 0 {
		return true
	}
	return (val-lo)%step == 0
}

// Run loops forever (until ctx is cancelled), checking every tickEvery
// whether any enabled job's schedule_cron is due "this minute", and firing
// it via engine.Start if so. A per-job in-memory map prevents firing twice
// within the same minute even with a sub-minute tick interval.
func Run(ctx context.Context, store *db.Store, engine *jobexec.Engine, agentID uuid.UUID, tickEvery time.Duration) {
	var mu sync.Mutex
	lastFired := map[uuid.UUID]string{} // jobID -> "YYYY-MM-DDTHH:MM" last fired

	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			minuteKey := now.Format("2006-01-02T15:04")
			jobs, err := store.ListSchedulableJobs(ctx, agentID)
			if err != nil {
				log.Println("scheduler: list schedulable jobs:", err)
				continue
			}
			for _, j := range jobs {
				if !Matches(j.Cron, now) {
					continue
				}
				mu.Lock()
				already := lastFired[j.ID] == minuteKey
				if !already {
					lastFired[j.ID] = minuteKey
				}
				mu.Unlock()
				if already {
					continue
				}
				log.Printf("scheduler: firing job %s (cron %q)", j.ID, j.Cron)
				if _, err := engine.Start(ctx, j.ID.String(), core.TriggerScheduled, ""); err != nil {
					log.Println("scheduler: start job:", err)
				}
			}
		}
	}
}
