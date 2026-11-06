package webui

import (
	"strings"
	"testing"
)

// renderDBForms is netfields_test.go's renderFileForms, for the database
// job forms instead (create lives inside the home page, edit is its own
// template) — same two things get proven: rendering succeeds at all
// (a malformed template only blows up on Execute, not at build time) and
// the network-folder fields follow the file job's platform rules exactly.
func renderDBForms(t *testing.T, platform string, job DBJobRequest) (create, edit string) {
	t.Helper()
	var cb, eb strings.Builder
	if err := homeTmpl.Execute(&cb, map[string]any{
		"Name": "agente", "Base": "", "CSRF": "t", "Platform": platform,
		"Activity": nil, "Jobs": nil,
	}); err != nil {
		t.Fatalf("formulário de criação não renderizou (platform=%q): %v", platform, err)
	}
	if err := editDBTmpl.Execute(&eb, map[string]any{
		"Name": "agente", "Base": "", "CSRF": "t", "JobID": "j1",
		"Job": job, "Platform": platform,
	}); err != nil {
		t.Fatalf("formulário de edição não renderizou (platform=%q): %v", platform, err)
	}
	return cb.String(), eb.String()
}

// Same Windows-only capability as the file job's network-folder fields
// (see TestNetworkFieldsShownPerPlatform): a database job's destination
// can also be a UNC path, so it needs the same credential inputs, hidden
// only for a confirmed Linux agent.
func TestDBNetworkFieldsShownPerPlatform(t *testing.T) {
	cases := []struct {
		platform string
		want     bool
		why      string
	}{
		{"windows", true, "agente Windows: é onde a função existe"},
		{"linux", false, "agente Linux: não implementa, não deve aparecer"},
		{"", true, "plataforma desconhecida: mostrar rotulado, nunca sumir com a função"},
	}
	for _, c := range cases {
		create, edit := renderDBForms(t, c.platform, DBJobRequest{})
		for name, out := range map[string]string{"criar": create, "editar": edit} {
			got := strings.Count(out, `name="network_user"`) > 0
			if got != c.want {
				t.Errorf("form %s (db), platform=%q: campos de rede presentes=%v, queria %v (%s)",
					name, c.platform, got, c.want, c.why)
			}
		}
	}
}

func TestDBUnknownPlatformIsLabelled(t *testing.T) {
	create, edit := renderDBForms(t, "", DBJobRequest{})
	for name, out := range map[string]string{"criar": create, "editar": edit} {
		if !strings.Contains(out, "somente agentes Windows") {
			t.Errorf("form %s (db): plataforma desconhecida deveria rotular o campo como Windows-only", name)
		}
	}
}

// Blank-keeps-the-secret convention, same as the file job: a stored
// network password must never render back into the edit page.
func TestDBNetworkPasswordNeverRendered(t *testing.T) {
	const secret = "senha-super-secreta-456"
	job := DBJobRequest{
		NetworkUser:        "Administrador",
		NetworkPassword:    secret, // nunca deveria chegar aqui, mas se chegar...
		HasNetworkPassword: true,
	}
	_, edit := renderDBForms(t, "windows", job)

	if strings.Contains(edit, secret) {
		t.Fatal("a senha do compartilhamento vazou para o HTML do formulário de banco de dados")
	}
	if !strings.Contains(edit, `value="Administrador"`) {
		t.Error("o usuário deveria vir preenchido na edição")
	}
	if !strings.Contains(edit, "deixe em branco para manter a atual") {
		t.Error("com senha guardada, o campo deve explicar que em branco mantém a atual")
	}

	_, editNoPass := renderDBForms(t, "windows", DBJobRequest{NetworkUser: "x"})
	if strings.Contains(editNoPass, "deixe em branco para manter a atual") {
		t.Error("sem senha guardada esse aviso engana: não deve aparecer")
	}
}
