package webui

import (
	"fmt"
	"time"
)

// TimezoneLabel turns what an agent reported about its clock into the one
// sentence that belongs next to a schedule field.
//
// Why this exists at all: a cron expression is matched against the AGENT's
// clock, never the browser's. A box whose host runs UTC while the agent
// container runs America/Sao_Paulo fires "03:00" three hours from when the
// operator meant it, and the only symptom is a backup that quietly never
// happens at the expected time. The schedule widget therefore always states
// which clock it is talking about.
//
// name is the agent's IANA zone name (preferred) or an abbreviation;
// offsetMinutes is the offset it last reported, used only when the name
// cannot be resolved here. An empty name means the agent has not reported
// one — an older build — and the caller gets an explicit "unknown" sentence
// instead of a confident guess, because showing the wrong timezone is worse
// than admitting we don't know it.
func TimezoneLabel(name string, offsetMinutes int, now time.Time) string {
	if name == "" {
		return "Os horários são interpretados no fuso do agente (este agente ainda não informou qual)."
	}
	// Resolving the name locally recomputes the offset for right now, so
	// the label survives a daylight-saving change the agent has not polled
	// through yet. Falling back to the reported offset keeps an agent whose
	// zone this machine lacks tzdata for from losing the information.
	offset := offsetMinutes
	if loc, err := time.LoadLocation(name); err == nil {
		_, seconds := now.In(loc).Zone()
		offset = seconds / 60
	}
	return fmt.Sprintf("Os horários são interpretados no fuso do agente: %s (%s).", name, utcOffset(offset))
}

// utcOffset formats minutes east of UTC as UTC-03:00 / UTC+05:30 / UTC+00:00.
func utcOffset(minutes int) string {
	sign := "+"
	if minutes < 0 {
		sign = "-"
		minutes = -minutes
	}
	return fmt.Sprintf("UTC%s%02d:%02d", sign, minutes/60, minutes%60)
}
