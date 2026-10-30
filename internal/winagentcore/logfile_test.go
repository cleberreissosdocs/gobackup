package winagentcore

import (
	"log"
	"os"
	"strings"
	"testing"
)

// The log exists to answer "what happened at 3am last night", so the two
// things that must hold are: lines actually land in the file, and the file
// cannot grow until it fills the server's system drive.
func TestFileLoggingWritesToProgramData(t *testing.T) {
	t.Setenv("ProgramData", t.TempDir())
	defer log.SetOutput(os.Stderr)

	if err := SetupFileLogging(); err != nil {
		t.Fatalf("configurar o log: %v", err)
	}
	log.Printf("gobackup: agendador: disparando %q", "tarefa-noturna")

	data, err := os.ReadFile(LogPath())
	if err != nil {
		t.Fatalf("ler o log: %v", err)
	}
	if !strings.Contains(string(data), "tarefa-noturna") {
		t.Errorf("a mensagem não chegou ao log, conteúdo: %q", string(data))
	}
}

func TestLogRotatesAndKeepsOneGeneration(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/agent.log"
	w, err := newRotatingWriter(path)
	if err != nil {
		t.Fatalf("abrir: %v", err)
	}

	line := []byte(strings.Repeat("x", 64*1024) + "\n")
	for written := 0; written < maxLogBytes*2; written += len(line) {
		if _, err := w.Write(line); err != nil {
			t.Fatalf("escrever: %v", err)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("log atual: %v", err)
	}
	if info.Size() > maxLogBytes {
		t.Errorf("o log passou do limite: %d bytes", info.Size())
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("a geração anterior deveria ter sido preservada: %v", err)
	}
	// Exactly one generation: no .2 piling up forever.
	if _, err := os.Stat(path + ".2"); err == nil {
		t.Error("deveria guardar só uma geração anterior, encontrou .2")
	}
}

// Appending across restarts matters: a service that truncated its log on
// every start would erase the evidence of the crash that restarted it.
func TestLogAppendsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/agent.log"

	w1, err := newRotatingWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Write([]byte("primeira execução\n")); err != nil {
		t.Fatal(err)
	}
	_ = w1.f.Close()

	w2, err := newRotatingWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Write([]byte("segunda execução\n")); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	for _, want := range []string{"primeira execução", "segunda execução"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("faltou %q no log: %q", want, string(data))
		}
	}
}
