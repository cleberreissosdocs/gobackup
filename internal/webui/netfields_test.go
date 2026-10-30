package webui

import (
	"strings"
	"testing"
)

// renderFileForms executes the two file-job forms (create lives inside the
// home page, edit is its own template) with the given platform and returns
// both outputs. Rendering them at all is itself part of the check: a
// malformed template only blows up when executed, never at build time.
func renderFileForms(t *testing.T, platform string, job FileJobRequest) (create, edit string) {
	t.Helper()
	var cb, eb strings.Builder
	if err := homeTmpl.Execute(&cb, map[string]any{
		"Name": "agente", "Base": "", "CSRF": "t", "Platform": platform,
		"Activity": nil, "Jobs": nil,
	}); err != nil {
		t.Fatalf("formulário de criação não renderizou (platform=%q): %v", platform, err)
	}
	if err := editFileTmpl.Execute(&eb, map[string]any{
		"Name": "agente", "Base": "", "CSRF": "t", "JobID": "j1",
		"Job": job, "Platform": platform,
	}); err != nil {
		t.Fatalf("formulário de edição não renderizou (platform=%q): %v", platform, err)
	}
	return cb.String(), eb.String()
}

// The network-folder fields are a Windows-agent capability. Showing them
// while administering a Linux agent would offer something that silently
// never runs; hiding them when the platform is merely UNKNOWN would be
// worse, since it would make a real capability disappear because of a
// lookup failure. So: hidden only for a confirmed Linux agent.
func TestNetworkFieldsShownPerPlatform(t *testing.T) {
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
		create, edit := renderFileForms(t, c.platform, FileJobRequest{})
		for name, out := range map[string]string{"criar": create, "editar": edit} {
			got := strings.Contains(out, `name="network_user"`)
			if got != c.want {
				t.Errorf("form %s, platform=%q: campos de rede presentes=%v, queria %v (%s)",
					name, c.platform, got, c.want, c.why)
			}
		}
	}
}

// When the platform is unknown the fields must say so, otherwise an
// operator on a Linux agent whose platform hasn't been reported yet would
// fill them in and wonder why nothing happened.
func TestUnknownPlatformIsLabelled(t *testing.T) {
	create, edit := renderFileForms(t, "", FileJobRequest{})
	for name, out := range map[string]string{"criar": create, "editar": edit} {
		if !strings.Contains(out, "somente agentes Windows") {
			t.Errorf("form %s: plataforma desconhecida deveria rotular o campo como Windows-only", name)
		}
	}
	winCreate, _ := renderFileForms(t, "windows", FileJobRequest{})
	if strings.Contains(winCreate, "somente agentes Windows") {
		t.Error("num agente Windows confirmado o aviso é ruído: não deve aparecer")
	}
}

// The stored password must never be rendered back into the page. This is
// the project's blank-keeps-the-secret convention, and the only way to
// verify it is to put a password in the struct and confirm it is absent
// from the HTML.
func TestNetworkPasswordNeverRendered(t *testing.T) {
	const secret = "senha-super-secreta-123"
	job := FileJobRequest{
		NetworkUser:        "Administrador",
		NetworkPassword:    secret, // nunca deveria chegar aqui, mas se chegar...
		HasNetworkPassword: true,
	}
	_, edit := renderFileForms(t, "windows", job)

	if strings.Contains(edit, secret) {
		t.Fatal("a senha do compartilhamento vazou para o HTML do formulário")
	}
	if !strings.Contains(edit, `value="Administrador"`) {
		t.Error("o usuário deveria vir preenchido na edição")
	}
	if !strings.Contains(edit, "deixe em branco para manter a atual") {
		t.Error("com senha guardada, o campo deve explicar que em branco mantém a atual")
	}

	_, editNoPass := renderFileForms(t, "windows", FileJobRequest{NetworkUser: "x"})
	if strings.Contains(editNoPass, "deixe em branco para manter a atual") {
		t.Error("sem senha guardada esse aviso engana: não deve aparecer")
	}
}
