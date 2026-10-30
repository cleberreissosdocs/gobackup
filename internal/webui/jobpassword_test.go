package webui

import (
	"strings"
	"testing"
)

// The "Executar" button in the job list must NOT ask for a database
// password: the agent stores it with the job, so the box was both
// redundant and misleading (it suggested the password was required for
// every run, when leaving it blank was the normal case).
//
// This renders the real home template rather than asserting on the
// template source, so the test keeps protecting the rendered page if the
// markup is restructured.
func TestJobListDoesNotAskForDatabasePassword(t *testing.T) {
	data := map[string]any{
		"Name": "agente-de-teste",
		"Base": "",
		"CSRF": "token-de-teste",
		"Jobs": []map[string]any{
			{"ID": "1", "Name": "Backup do banco", "Kind": "database", "Enabled": true, "Status": "success", "Percent": -1, "Cron": ""},
			{"ID": "2", "Name": "Backup de arquivos", "Kind": "files", "Enabled": true, "Status": "success", "Percent": -1, "Cron": ""},
		},
	}

	var sb strings.Builder
	if err := homeTmpl.Execute(&sb, data); err != nil {
		t.Fatalf("renderizar a página inicial: %v", err)
	}
	html := sb.String()

	if !strings.Contains(html, "Backup do banco") {
		t.Fatal("a tarefa de banco não foi renderizada; o teste não está olhando a página certa")
	}
	if strings.Contains(html, `placeholder="senha (se necessário)"`) {
		t.Error("a lista de tarefas ainda pede a senha do banco ao executar")
	}
	// Only the RUN forms matter here: the same page also carries the
	// forms that CREATE a database job, and those must keep their
	// password field.
	for _, trecho := range runForms(html) {
		if strings.Contains(trecho, `name="password"`) {
			t.Errorf("o formulário de executar ainda pede senha: %s", trecho)
		}
	}

	// The run button itself must survive the removal.
	if !strings.Contains(html, "Executar") {
		t.Error("o botão Executar sumiu junto com o campo de senha")
	}

	// Guard the assertion above: if runForms stops finding anything the
	// loop would pass vacuously and the test would protect nothing.
	if len(runForms(html)) == 0 {
		t.Error("nenhum formulário de executar encontrado; o teste passaria sem verificar nada")
	}
}

// runForms returns each <form ... action=".../run"> block found in the page.
func runForms(html string) []string {
	var out []string
	resto := html
	for {
		i := strings.Index(resto, `/run"`)
		if i < 0 {
			return out
		}
		inicio := strings.LastIndex(resto[:i], "<form")
		fim := strings.Index(resto[i:], "</form>")
		if inicio < 0 || fim < 0 {
			return out
		}
		out = append(out, resto[inicio:i+fim])
		resto = resto[i+fim:]
	}
}
