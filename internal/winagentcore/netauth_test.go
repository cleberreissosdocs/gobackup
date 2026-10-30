package winagentcore

import "testing"

// UNCServerRoot decides whether a path needs SMB authentication at all and
// which share to authenticate against. Getting it wrong is silent: an
// unrecognized UNC path makes ConnectUNC a no-op, and the job then fails
// much later with a confusing "path not found" instead of a credentials
// error — so the edge cases are pinned here.
func TestUNCServerRoot(t *testing.T) {
	cases := []struct {
		in, want, why string
	}{
		{`\\srv\share\sub\dir`, `\\srv\share`, "reduz a um prefixo servidor+share"},
		{`\\srv\share`, `\\srv\share`, "já é o prefixo"},
		{`\\srv\share\`, `\\srv\share`, "barra final"},
		{`//srv/share/sub`, `\\srv\share`, "aceita barras normais"},
		{`  \\srv\share  `, `\\srv\share`, "espaços em volta"},
		{`\\\\srv\\share\\sub`, `\\srv\share`, "barras duplicadas por escape"},
		{`C:\dados`, "", "caminho local não precisa autenticar"},
		{`C:\`, "", "raiz local"},
		{"", "", "vazio"},
		{`\\srv`, "", "servidor sem share não é autenticável"},
		{`\\`, "", "só separador"},
		{`\\srv\`, "", "share vazio"},
	}
	for _, c := range cases {
		if got := UNCServerRoot(c.in); got != c.want {
			t.Errorf("UNCServerRoot(%q) = %q, want %q (%s)", c.in, got, c.want, c.why)
		}
	}
}

// qualifyUser is what makes a plain "Administrador" work against a
// workgroup file server. Proven necessary on a real server running as
// Local System: unqualified fails with Windows error 1312, qualified
// authenticates. See the function's own doc comment.
func TestQualifyUser(t *testing.T) {
	cases := []struct {
		share, user, want, why string
	}{
		{`\\192.168.8.250\repositorio`, "Administrador", `192.168.8.250\Administrador`, "nome simples ganha o servidor"},
		{`\\fileserver\dados`, "backup", `fileserver\backup`, "servidor por nome"},
		{`\\srv\s`, `OUTRO\usuario`, `OUTRO\usuario`, "já qualificado com domínio: não mexe"},
		{`\\srv\s`, "user@dominio.local", "user@dominio.local", "UPN: não mexe"},
		{"", "Administrador", "Administrador", "sem share não há o que qualificar"},
	}
	for _, c := range cases {
		if got := qualifyUser(c.share, c.user); got != c.want {
			t.Errorf("qualifyUser(%q, %q) = %q, want %q (%s)", c.share, c.user, got, c.want, c.why)
		}
	}
}

// ConnectUNC must be safe to call unconditionally before every file job,
// including for ordinary local paths and for UNC paths with no credentials
// configured — otherwise every caller would need its own guard.
func TestConnectUNCIsNoOpWithoutWork(t *testing.T) {
	if err := ConnectUNC(`C:\dados`, "user", "pass"); err != nil {
		t.Errorf("caminho local deve ser no-op, got: %v", err)
	}
	if err := ConnectUNC(`\\srv\share`, "", ""); err != nil {
		t.Errorf("sem usuário deve ser no-op, got: %v", err)
	}
	if err := DisconnectUNC(`C:\dados`); err != nil {
		t.Errorf("desconectar caminho local deve ser no-op, got: %v", err)
	}
}

// An empty secret is "nothing stored", not an encrypted empty string —
// callers rely on "" round-tripping to "" on every platform so a job
// without network credentials is distinguishable from one whose password
// failed to decrypt.
func TestEmptySecretRoundTrips(t *testing.T) {
	enc, err := EncryptSecret("")
	if err != nil || enc != "" {
		t.Fatalf("EncryptSecret(\"\") = %q, %v; want \"\", nil", enc, err)
	}
	dec, err := DecryptSecret("")
	if err != nil || dec != "" {
		t.Fatalf("DecryptSecret(\"\") = %q, %v; want \"\", nil", dec, err)
	}
}
