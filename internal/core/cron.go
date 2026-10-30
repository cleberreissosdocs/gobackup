// Cron matching for scheduled jobs, kept in core (which has no database
// or OS-specific dependencies) so BOTH the Linux agent's scheduler and
// the Windows agent's can use it. It previously lived in package
// scheduler, which imports the Postgres driver and the Linux job engine
// -- importing that from the Windows agent would have pulled a database
// stack into a binary that stores its jobs in a JSON file.
//
// This intentionally doesn't aim for full cron spec coverage (no named
// months/weekdays, no "L"/"W"/"#" extensions) -- numbers, "*", comma
// lists, ranges ("a-b") and steps ("*/N", "a-b/N") cover the vast
// majority of real schedules without an external dependency.
package core

import (
	"strconv"
	"strings"
	"time"
)

// CronMatches reports whether cron expression expr is due at time t (minute
// granularity — seconds are ignored). Returns false for a malformed
// expression rather than erroring, so one bad job can't crash the loop.
func CronMatches(expr string, t time.Time) bool {
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
