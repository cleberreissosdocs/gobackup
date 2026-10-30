package db

import (
	"bytes"
	"crypto/rand"
	"io"
	"strings"
	"testing"
)

func chaveDeTeste(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		t.Fatalf("gerar chave: %v", err)
	}
	return k
}

func TestCifraIdaEVolta(t *testing.T) {
	key := chaveDeTeste(t)
	for _, senha := range []string{
		"simples",
		"com espaço e acento: çãõ",
		"s0s@d0cs*#%&+-=?@_",
		strings.Repeat("longa", 200),
	} {
		sealed, err := encryptSecret(key, senha)
		if err != nil {
			t.Fatalf("cifrar %q: %v", senha, err)
		}
		if bytes.Contains(sealed, []byte(senha)) {
			t.Errorf("a senha %q aparece em texto puro no valor cifrado", senha)
		}
		volta, err := decryptSecret(key, sealed)
		if err != nil {
			t.Fatalf("decifrar %q: %v", senha, err)
		}
		if volta != senha {
			t.Errorf("voltou %q, esperado %q", volta, senha)
		}
	}
}

// Two encryptions of the same password must differ, or equal passwords
// become visibly equal in the table.
func TestCifraNaoRepeteSaida(t *testing.T) {
	key := chaveDeTeste(t)
	a, err := encryptSecret(key, "mesma-senha")
	if err != nil {
		t.Fatal(err)
	}
	b, err := encryptSecret(key, "mesma-senha")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("duas cifragens da mesma senha deram o mesmo resultado; o nonce não está variando")
	}
}

// A changed key must produce an error the operator can act on, not a
// bare cryptographic message.
func TestChaveTrocadaExplicaOQueFazer(t *testing.T) {
	sealed, err := encryptSecret(chaveDeTeste(t), "segredo")
	if err != nil {
		t.Fatal(err)
	}
	_, err = decryptSecret(chaveDeTeste(t), sealed)
	if err == nil {
		t.Fatal("decifrar com outra chave deveria falhar")
	}
	if !strings.Contains(err.Error(), "digitada de novo") {
		t.Errorf("a mensagem não diz o que fazer: %v", err)
	}
}

func TestValorTruncadoNaoEntraEmPanico(t *testing.T) {
	key := chaveDeTeste(t)
	for _, v := range [][]byte{nil, {}, {1, 2, 3}} {
		if _, err := decryptSecret(key, v); err == nil {
			t.Errorf("valor de %d bytes deveria falhar", len(v))
		}
	}
}

// Tampering must be rejected: GCM authenticates, so a flipped byte in
// the stored column cannot yield a silently wrong password.
func TestAlteracaoNoBancoEhRejeitada(t *testing.T) {
	key := chaveDeTeste(t)
	sealed, err := encryptSecret(key, "senha-original")
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 0x01
	if _, err := decryptSecret(key, sealed); err == nil {
		t.Error("um byte alterado passou sem erro")
	}
}
