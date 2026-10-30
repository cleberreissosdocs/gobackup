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
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/jobexec"
)

// Matches reports whether cron expression expr is due at time t.
//
// The implementation moved to core.CronMatches so the Windows agent can
// share it (see internal/core/cron.go); this stays as the name the
// agent's scheduler and its tests already use.
func Matches(expr string, t time.Time) bool {
	return core.CronMatches(expr, t)
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
