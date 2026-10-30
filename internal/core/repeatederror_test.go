package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// coletor captura as linhas que o laço teria escrito no log.
type coletor struct{ linhas []string }

func (c *coletor) logf(format string, args ...any) {
	c.linhas = append(c.linhas, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func TestMesmoErroEhRegistradoUmaVezSo(t *testing.T) {
	var c coletor
	var r RepeatedError
	falha := errors.New("dial tcp 127.0.0.1:5432: connection refused")

	for i := 0; i < 50; i++ {
		r.Report("offline checker", falha, c.logf)
	}

	if len(c.linhas) != 1 {
		t.Fatalf("esperado 1 linha para 50 falhas iguais, veio %d: %v", len(c.linhas), c.linhas)
	}
	if !strings.Contains(c.linhas[0], "connection refused") {
		t.Errorf("a única linha tem de conter o erro real: %q", c.linhas[0])
	}
}

func TestRecuperacaoDizQuantoDurouEQuantasFalharam(t *testing.T) {
	var c coletor
	relogio := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	r := RepeatedError{Now: func() time.Time { return relogio }}

	r.Report("offline checker", errors.New("connection refused"), c.logf)
	for i := 0; i < 9; i++ {
		r.Report("offline checker", errors.New("connection refused"), c.logf)
	}
	relogio = relogio.Add(95 * time.Second)
	r.Resolved("offline checker", c.logf)

	if len(c.linhas) != 2 {
		t.Fatalf("esperado abertura + recuperação, veio %d: %v", len(c.linhas), c.linhas)
	}
	rec := c.linhas[1]
	for _, parte := range []string{"recovered after", "1m35s", "10 failed attempt"} {
		if !strings.Contains(rec, parte) {
			t.Errorf("a linha de recuperação precisa conter %q: %q", parte, rec)
		}
	}
}

func TestSucessoSemFalhaAnteriorNaoEscreveNada(t *testing.T) {
	var c coletor
	var r RepeatedError
	r.Resolved("offline checker", c.logf)
	if len(c.linhas) != 0 {
		t.Errorf("laço saudável tem de ficar calado, veio: %v", c.linhas)
	}
}

// Um erro DIFERENTE é notícia: tem de aparecer, e o anterior não pode
// sumir sem dizer quantas vezes se repetiu.
func TestErroDiferenteEhRegistradoEContabilizaOAnterior(t *testing.T) {
	var c coletor
	var r RepeatedError

	r.Report("heartbeat", errors.New("connection refused"), c.logf)
	for i := 0; i < 4; i++ {
		r.Report("heartbeat", errors.New("connection refused"), c.logf)
	}
	r.Report("heartbeat", errors.New("certificate expired"), c.logf)

	if len(c.linhas) != 3 {
		t.Fatalf("esperado 3 linhas (1º erro, resumo do anterior, 2º erro), veio %d: %v", len(c.linhas), c.linhas)
	}
	if !strings.Contains(c.linhas[1], "repeated 4 more time") {
		t.Errorf("o resumo do erro anterior está errado: %q", c.linhas[1])
	}
	if !strings.Contains(c.linhas[2], "certificate expired") {
		t.Errorf("o erro novo tem de aparecer: %q", c.linhas[2])
	}
}

// Depois de recuperar, uma falha nova volta a ser registrada — senão a
// segunda queda passaria despercebida.
func TestNovaQuedaAposRecuperarVoltaARegistrar(t *testing.T) {
	var c coletor
	var r RepeatedError
	falha := errors.New("connection refused")

	r.Report("offline checker", falha, c.logf)
	r.Resolved("offline checker", c.logf)
	r.Report("offline checker", falha, c.logf)

	if len(c.linhas) != 3 {
		t.Fatalf("esperado queda, recuperação e nova queda, veio %d: %v", len(c.linhas), c.linhas)
	}
	if !strings.Contains(c.linhas[2], "connection refused") {
		t.Errorf("a segunda queda tem de aparecer: %q", c.linhas[2])
	}
}

func TestErroNuloNaoRegistraNada(t *testing.T) {
	var c coletor
	var r RepeatedError
	r.Report("offline checker", nil, c.logf)
	if len(c.linhas) != 0 {
		t.Errorf("err nil não pode gerar linha: %v", c.linhas)
	}
}
