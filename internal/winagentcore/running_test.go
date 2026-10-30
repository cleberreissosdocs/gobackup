package winagentcore

import (
	"testing"
	"time"
)

// newTestStore points the real constructor at a temp dir, so these tests
// exercise the same path resolution the agent uses rather than a
// hand-built struct that could drift from it.
func newTestStore(t *testing.T) *JobStore {
	t.Helper()
	t.Setenv("ProgramData", t.TempDir())
	return NewJobStore()
}

// Central decides whether to show "Executando" (and to link the progress
// page) from last_status in the agent's list_jobs response. The Windows
// agent only ever wrote terminal statuses, so a running job was invisible
// there no matter how long it ran — the Linux agent got this for free by
// recording a job_executions row at start.
func TestMarkRunningIsVisibleAndLeavesHistoryAlone(t *testing.T) {
	s := newTestStore(t)
	j, err := s.Create(Job{Kind: "files", Name: "tarefa", Source: `C:\a`, Dest: `D:\b`})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.MarkRunning(j.ID); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Get(j.ID)
	if got.LastStatus != "running" {
		t.Fatalf("last_status = %q, want \"running\" — sem isso o Central nunca mostra \"Executando\"", got.LastStatus)
	}

	// Only terminal states belong in the timeline; a job that merely
	// started must not draw a zero-length bar on the dashboard.
	hist, err := s.ListHistory(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 0 {
		t.Fatalf("MarkRunning gravou %d entrada(s) de histórico; não deveria gravar nenhuma", len(hist))
	}

	// Finishing still records history as before.
	if err := s.UpdateStatus(j.ID, "success", "", 10, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if hist, _ = s.ListHistory(time.Now().Add(-time.Hour)); len(hist) != 1 {
		t.Fatalf("ao concluir, esperava 1 entrada de histórico, veio %d", len(hist))
	}
}

// A crash or service restart mid-backup leaves "running" persisted with no
// goroutine left to clear it. Central would then show the job executing
// forever AND the UI would keep its Executar button disabled permanently —
// a stuck flag nothing clears on its own.
func TestResetStaleRunningUnsticksAfterRestart(t *testing.T) {
	s := newTestStore(t)
	running, _ := s.Create(Job{Kind: "files", Name: "interrompida", Source: `C:\a`, Dest: `D:\b`})
	done, _ := s.Create(Job{Kind: "files", Name: "concluida", Source: `C:\c`, Dest: `D:\d`})

	if err := s.MarkRunning(running.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(done.ID, "success", "", 1, 1, time.Now()); err != nil {
		t.Fatal(err)
	}

	n, err := s.ResetStaleRunning()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("corrigiu %d tarefa(s), esperava 1", n)
	}

	got, _, _ := s.Get(running.ID)
	if got.LastStatus == "running" {
		t.Error("tarefa continuou presa em \"running\" após o reset")
	}
	if got.LastError == "" {
		t.Error("deveria explicar por que foi marcada como falha")
	}

	// A job that finished normally must not be rewritten.
	if other, _, _ := s.Get(done.ID); other.LastStatus != "success" {
		t.Errorf("tarefa concluída virou %q; o reset só deve tocar nas presas em running", other.LastStatus)
	}

	// Idempotent: running it again on clean state changes nothing.
	if n, _ := s.ResetStaleRunning(); n != 0 {
		t.Errorf("segunda passada corrigiu %d; deveria ser 0", n)
	}
}

// A second run request for a job already running must be refused by the
// AGENT, not merely discouraged by a disabled button: a double-click, a
// direct POST, or a schedule firing during a manual run would otherwise
// start the same backup twice against the same destination.
func TestTryReserveRefusesSecondRun(t *testing.T) {
	e := &Executor{}
	if !e.tryReserve("job-1") {
		t.Fatal("a primeira reserva deveria ser aceita")
	}
	if e.tryReserve("job-1") {
		t.Fatal("a segunda reserva da MESMA tarefa deveria ser recusada")
	}
	if !e.tryReserve("job-2") {
		t.Fatal("outra tarefa não deveria ser bloqueada")
	}
	// Once it finishes, the job can run again.
	e.unregisterControl("job-1")
	if !e.tryReserve("job-1") {
		t.Fatal("após terminar, a tarefa deveria poder rodar de novo")
	}
}
