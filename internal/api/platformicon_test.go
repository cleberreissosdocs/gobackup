package api

import (
	"strings"
	"testing"
)

// The dashboard marks each agent's OS with an inline SVG. These assertions
// exist because the icon is decoration: it must never become a visual bug
// (a missing-glyph box, a stray marker on an agent whose OS is unknown) in
// a table the operator watches all day.
func TestPlatformIcon(t *testing.T) {
	win := string(platformIcon("windows"))
	if !strings.Contains(win, "<svg") || !strings.Contains(win, "<title>Windows</title>") {
		t.Errorf("windows deveria render um SVG identificado, veio: %.80q", win)
	}

	lin := string(platformIcon("linux"))
	if !strings.Contains(lin, "<svg") || !strings.Contains(lin, "<title>Linux</title>") {
		t.Errorf("linux deveria render um SVG identificado, veio: %.80q", lin)
	}

	if win == lin {
		t.Error("os dois sistemas estão desenhando o mesmo ícone")
	}

	// An agent on a build that predates platform reporting gets nothing:
	// a "?" marker would be the loudest thing in the row while meaning the
	// least.
	for _, unknown := range []string{"", "freebsd", "darwin"} {
		if got := string(platformIcon(unknown)); got != "" {
			t.Errorf("plataforma %q deveria render nada, veio: %.60q", unknown, got)
		}
	}
}

// The icon has to actually reach the page, as markup rather than escaped
// text — returning template.HTML is what makes that work, and it is easy
// to break by switching the helper to a plain string.
func TestDashboardRendersPlatformIcons(t *testing.T) {
	var b strings.Builder
	err := dashboardTmpl.Execute(&b, map[string]any{
		"CSRF": "t",
		"Agents": []dashboardAgentView{
			{ID: "a1", Name: "servidor-linux", Hostname: "h1", Status: "online", Platform: "linux", LastSeen: "agora"},
			{ID: "a2", Name: "servidor-windows", Hostname: "h2", Status: "online", Platform: "windows", LastSeen: "agora"},
			{ID: "a3", Name: "servidor-antigo", Hostname: "h3", Status: "online", Platform: "", LastSeen: "agora"},
		},
		"Jobs": nil,
	})
	if err != nil {
		t.Fatalf("dashboard não renderizou: %v", err)
	}
	out := b.String()

	if strings.Contains(out, "&lt;svg") {
		t.Fatal("o SVG saiu escapado como texto — o helper precisa devolver template.HTML")
	}
	if n := strings.Count(out, "<svg"); n != 2 {
		t.Errorf("esperava 2 ícones (um linux, um windows), encontrei %d", n)
	}
	if !strings.Contains(out, "<title>Linux</title>") || !strings.Contains(out, "<title>Windows</title>") {
		t.Error("faltou um dos dois ícones na página")
	}
	// All three agents still render, icon or not.
	for _, name := range []string{"servidor-linux", "servidor-windows", "servidor-antigo"} {
		if !strings.Contains(out, name) {
			t.Errorf("agente %q sumiu da tabela", name)
		}
	}
}
