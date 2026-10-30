package winagentcore

import (
	"errors"
	"testing"
	"time"

	"github.com/cleber/gobackup/internal/poller"
)

func schedTestStore(t *testing.T) *JobStore {
	t.Helper()
	t.Setenv("ProgramData", t.TempDir())
	return NewJobStore()
}

// fires records what the scheduler asked to start.
type fires struct {
	ids []string
	err error
}

func (f *fires) start(jobID, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.ids = append(f.ids, jobID)
	return nil
}

func newTestScheduler(t *testing.T, store *JobStore, f *fires) *Scheduler {
	t.Helper()
	return &Scheduler{store: store, start: f.start, lastFired: map[string]string{}}
}

func mustCreate(t *testing.T, s *JobStore, j Job) Job {
	t.Helper()
	created, err := s.Create(j)
	if err != nil {
		t.Fatalf("criar tarefa: %v", err)
	}
	return created
}

func schedAt(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// The whole point of the feature: a job with a cron actually fires.
func TestSchedulerFiresDueJob(t *testing.T) {
	store := schedTestStore(t)
	j := mustCreate(t, store, Job{Name: "diario", Kind: "files", Cron: "0 3 * * *", Enabled: true})
	f := &fires{}
	s := newTestScheduler(t, store, f)

	s.RunOnce(schedAt(t, "2026-10-30 03:00"))

	if len(f.ids) != 1 || f.ids[0] != j.ID {
		t.Fatalf("esperava disparar a tarefa %s, disparou %v", j.ID, f.ids)
	}
}

// The tick is sub-minute, so without per-minute bookkeeping a 3am job
// would start three times at 3am -- three concurrent backups of the same
// thing.
func TestSchedulerFiresOncePerMinute(t *testing.T) {
	store := schedTestStore(t)
	mustCreate(t, store, Job{Name: "diario", Kind: "files", Cron: "0 3 * * *", Enabled: true})
	f := &fires{}
	s := newTestScheduler(t, store, f)

	base := schedAt(t, "2026-10-30 03:00")
	s.RunOnce(base)
	s.RunOnce(base.Add(20 * time.Second))
	s.RunOnce(base.Add(40 * time.Second))

	if len(f.ids) != 1 {
		t.Fatalf("esperava 1 disparo no mesmo minuto, houve %d", len(f.ids))
	}

	// ...but the next day it must fire again: bookkeeping that never
	// expired would silently stop the schedule after its first run.
	s.RunOnce(schedAt(t, "2026-10-31 03:00"))
	if len(f.ids) != 2 {
		t.Fatalf("esperava disparar de novo no dia seguinte, total=%d", len(f.ids))
	}
}

func TestSchedulerSkipsDisabledAndUnscheduled(t *testing.T) {
	store := schedTestStore(t)
	mustCreate(t, store, Job{Name: "desativada", Kind: "files", Cron: "0 3 * * *", Enabled: false})
	mustCreate(t, store, Job{Name: "sem agendamento", Kind: "files", Cron: "", Enabled: true})
	mustCreate(t, store, Job{Name: "agendamento invalido", Kind: "files", Cron: "todo dia", Enabled: true})
	f := &fires{}
	s := newTestScheduler(t, store, f)

	s.RunOnce(schedAt(t, "2026-10-30 03:00"))

	if len(f.ids) != 0 {
		t.Fatalf("não deveria disparar nada, disparou %v", f.ids)
	}
}

// A backup that takes longer than its own interval must not stack up: the
// refusal is logged and the schedule simply tries again next tick.
func TestSchedulerSurvivesRefusedStart(t *testing.T) {
	store := schedTestStore(t)
	mustCreate(t, store, Job{Name: "demorada", Kind: "files", Cron: "* * * * *", Enabled: true})
	f := &fires{err: errors.New("esta tarefa já está em execução")}
	s := newTestScheduler(t, store, f)

	// Must not panic, and must keep running for the next minute.
	s.RunOnce(schedAt(t, "2026-10-30 03:00"))
	f.err = nil
	s.RunOnce(schedAt(t, "2026-10-30 03:01"))

	if len(f.ids) != 1 {
		t.Fatalf("esperava que o minuto seguinte voltasse a disparar, ids=%v", f.ids)
	}
}

// Bookkeeping must not grow without bound in a service that runs for
// months.
func TestSchedulerForgetsOldMinutes(t *testing.T) {
	store := schedTestStore(t)
	mustCreate(t, store, Job{Name: "minuto", Kind: "files", Cron: "* * * * *", Enabled: true})
	f := &fires{}
	s := newTestScheduler(t, store, f)

	start := schedAt(t, "2026-10-30 03:00")
	for i := 0; i < 50; i++ {
		s.RunOnce(start.Add(time.Duration(i) * time.Minute))
	}

	if len(f.ids) != 50 {
		t.Fatalf("esperava 50 disparos (um por minuto), houve %d", len(f.ids))
	}
	if got := len(s.lastFired); got > 1 {
		t.Errorf("registro deveria guardar só o minuto corrente, guardou %d entradas", got)
	}
}

// Editing a job and leaving the password blank must not wipe the stored
// credential -- a scheduled database backup would start failing at 3am,
// silently, because someone renamed the job.
func TestUpdateJobKeepsStoredDBPassword(t *testing.T) {
	store := schedTestStore(t)
	j := mustCreate(t, store, Job{
		Name: "banco", Kind: "database", Engine: "postgresql",
		Database: "app", Cron: "0 3 * * *", Enabled: true,
		DBPasswordEnc: "cifrado-existente",
	})

	e := &Executor{Store: store, Registry: NewProgressRegistry()}
	res := e.Execute(poller.QueuedCommand{ID: "c1", Type: "update_job", Payload: map[string]any{
		"job_id": j.ID, "name": "banco renomeado", "engine": "postgresql",
		"database": "app", "cron": "0 3 * * *", "enabled": true,
		"password": "", // em branco = manter
	}})
	if res.Status != "acked" {
		t.Fatalf("update_job falhou: %v", res.Result)
	}

	got, found, err := store.Get(j.ID)
	if err != nil || !found {
		t.Fatalf("recarregar tarefa: %v (encontrada=%v)", err, found)
	}
	if got.Name != "banco renomeado" {
		t.Errorf("o nome deveria ter sido alterado, veio %q", got.Name)
	}
	if got.DBPasswordEnc != "cifrado-existente" {
		t.Errorf("a senha guardada foi perdida na edição: %q", got.DBPasswordEnc)
	}
}
