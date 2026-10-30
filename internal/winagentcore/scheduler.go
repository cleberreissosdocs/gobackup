package winagentcore

import (
	"log"
	"sync"
	"time"

	"github.com/cleber/gobackup/internal/core"
)

// schedulerTick is how often due jobs are looked for. Cron granularity is
// one minute, so anything below a minute is enough; 20s matches the Linux
// agent and gives three chances to catch a minute even if one tick is
// delayed by a slow disk or a long garbage collection.
const schedulerTick = 20 * time.Second

// Scheduler fires jobs whose cron expression is due.
//
// This exists because the Windows agent previously stored a job's cron,
// displayed it, and reported it to Central -- but nothing ever fired it.
// Every Windows backup was in practice manual, while the screen showed a
// schedule, which is the worst possible failure for a backup product: an
// operator who configured a nightly backup had every reason to believe it
// was running.
type Scheduler struct {
	store *JobStore

	// start launches a due job. It is a field rather than a direct call
	// to exec.StartJob so tests can drive the scheduling logic -- which
	// minute fires, what is skipped, what is refused -- without actually
	// spawning rsync against a real path.
	start func(jobID, password string) error

	mu sync.Mutex
	// lastFired maps job ID -> "YYYY-MM-DDTHH:MM" of its last firing, so
	// a job fires at most once per minute even though the tick is
	// sub-minute. Keyed by minute rather than by a timer so a tick that
	// arrives late (or twice) cannot double-fire.
	lastFired map[string]string
}

func NewScheduler(store *JobStore, exec *Executor) *Scheduler {
	return &Scheduler{store: store, start: exec.StartJob, lastFired: map[string]string{}}
}

// Run blocks, firing due jobs until stop is closed. Intended to be called
// in its own goroutine by the service's main loop.
func (s *Scheduler) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()
	for {
		s.RunOnce(time.Now())
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

// RunOnce fires every job due at now. Separated from Run so tests can
// drive it with an explicit time instead of waiting for a wall clock.
//
// now is local time on purpose: an operator who schedules "3am" means 3am
// where the server is, and the Windows agent runs on a machine already
// set to the operator's timezone.
func (s *Scheduler) RunOnce(now time.Time) {
	jobs, err := s.store.List()
	if err != nil {
		log.Printf("gobackup: agendador: não foi possível ler as tarefas: %v", err)
		return
	}
	minuteKey := now.Format("2006-01-02T15:04")

	for _, j := range jobs {
		if !j.Enabled || j.Cron == "" {
			continue
		}
		if !core.CronMatches(j.Cron, now) {
			continue
		}

		s.mu.Lock()
		already := s.lastFired[j.ID] == minuteKey
		if !already {
			s.lastFired[j.ID] = minuteKey
		}
		s.mu.Unlock()
		if already {
			continue
		}

		log.Printf("gobackup: agendador: disparando %q (agendamento %q)", j.Name, j.Cron)
		if err := s.start(j.ID, ""); err != nil {
			// The common case here is the previous run of the SAME job
			// still going (a backup that takes longer than its own
			// interval). That is a refusal, not a crash: it is logged and
			// the next tick tries again, which is exactly the behaviour
			// that keeps two rsyncs off one destination.
			log.Printf("gobackup: agendador: %q não iniciou: %v", j.Name, err)
		}
	}

	s.forget(minuteKey)
}

// forget drops bookkeeping for minutes that have passed and for jobs that
// no longer exist, so a long-lived service does not accumulate an entry
// per job per minute forever.
func (s *Scheduler) forget(currentMinute string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, minute := range s.lastFired {
		if minute != currentMinute {
			delete(s.lastFired, id)
		}
	}
}
