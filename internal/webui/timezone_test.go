package webui

import (
	"strings"
	"testing"
	"time"
)

// A cron expression is matched against the AGENT's clock. When the panel
// doesn't say which clock that is, someone types "03:00" meaning their own
// and the backup fires hours away -- silently, because nothing errors: the
// job just never runs when expected. These tests pin the sentence and its
// presence in every form that can set a schedule.

func TestFusoConhecidoApareceComNomeEOffset(t *testing.T) {
	agora := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	rotulo := TimezoneLabel("America/Sao_Paulo", -180, agora)

	if !strings.Contains(rotulo, "America/Sao_Paulo") {
		t.Errorf("o nome do fuso tem de aparecer: %q", rotulo)
	}
	if !strings.Contains(rotulo, "UTC-03:00") {
		t.Errorf("o offset tem de aparecer formatado: %q", rotulo)
	}
}

func TestFusoDesconhecidoDizQueNaoSabe(t *testing.T) {
	rotulo := TimezoneLabel("", 0, time.Now())

	// O erro grave aqui seria assumir UTC (ou o fuso de quem lê) e
	// mostrar um horário com cara de certeza.
	if strings.Contains(rotulo, "UTC+") || strings.Contains(rotulo, "UTC-") {
		t.Errorf("sem informação do agente, não pode exibir offset nenhum: %q", rotulo)
	}
	if !strings.Contains(rotulo, "ainda não informou") {
		t.Errorf("tem de dizer claramente que o fuso é desconhecido: %q", rotulo)
	}
}

// O offset é recalculado pelo nome, não repetido do que o agente mandou:
// senão o rótulo mente depois de uma virada de horário de verão que o
// agente ainda não reportou.
func TestOffsetERecalculadoPeloNomeENaoPeloValorReportado(t *testing.T) {
	if _, err := time.LoadLocation("America/New_York"); err != nil {
		t.Skip("sem tzdata nesta máquina")
	}
	verao := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)    // EDT, -04:00
	inverno := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC) // EST, -05:00

	// Em ambos os casos o agente reportou o offset de inverno.
	if got := TimezoneLabel("America/New_York", -300, verao); !strings.Contains(got, "UTC-04:00") {
		t.Errorf("no verão tem de recalcular para -04:00, veio %q", got)
	}
	if got := TimezoneLabel("America/New_York", -300, inverno); !strings.Contains(got, "UTC-05:00") {
		t.Errorf("no inverno tem de dar -05:00, veio %q", got)
	}
}

// Nome que esta máquina não resolve: usa o offset reportado em vez de
// descartar a informação.
func TestNomeNaoResolvivelUsaOffsetReportado(t *testing.T) {
	rotulo := TimezoneLabel("-03", -180, time.Now())
	if !strings.Contains(rotulo, "UTC-03:00") {
		t.Errorf("devia cair no offset reportado: %q", rotulo)
	}
}

func TestOffsetComMeiaHoraEZero(t *testing.T) {
	if got := utcOffset(330); got != "UTC+05:30" {
		t.Errorf("Índia: esperado UTC+05:30, veio %q", got)
	}
	if got := utcOffset(0); got != "UTC+00:00" {
		t.Errorf("UTC: esperado UTC+00:00, veio %q", got)
	}
	if got := utcOffset(-180); got != "UTC-03:00" {
		t.Errorf("Brasília: esperado UTC-03:00, veio %q", got)
	}
}

// Uma página que esqueça de passar AgentTZ não pode derrubar a tela inteira
// de tarefas por causa de um rótulo: degrada para "fuso desconhecido".
func TestPaginaSemAgentTZAindaRenderiza(t *testing.T) {
	var sb strings.Builder
	err := homeTmpl.Execute(&sb, map[string]any{
		"Name": "agente", "Base": "", "CSRF": "t", "Jobs": nil, "Platform": "linux",
	})
	if err != nil {
		t.Fatalf("sem AgentTZ a página tem de renderizar mesmo assim: %v", err)
	}
	if !strings.Contains(sb.String(), "ainda não informou") {
		t.Error("devia cair no texto de fuso desconhecido")
	}
}

// Renderiza os templates REAIS: template.Must só avalia no init do pacote,
// então um template malformado compila e só quebra em produção.
func TestTodoFormularioComAgendamentoMostraOFuso(t *testing.T) {
	rotulo := TimezoneLabel("America/Sao_Paulo", -180, time.Now())
	job := map[string]any{"Cron": "0 3 * * *", "Engine": "mysql", "Compression": "gzip", "DestType": "local"}

	paginas := map[string]func() (string, error){
		"criar tarefas (arquivos e banco)": func() (string, error) {
			var sb strings.Builder
			err := homeTmpl.Execute(&sb, map[string]any{
				"Name": "agente", "Base": "", "CSRF": "t", "Jobs": nil,
				"Platform": "linux", "AgentTZ": rotulo,
			})
			return sb.String(), err
		},
		"editar tarefa de arquivos": func() (string, error) {
			var sb strings.Builder
			err := editFileTmpl.Execute(&sb, map[string]any{
				"Name": "agente", "Base": "", "CSRF": "t", "JobID": "1", "Job": job,
				"Platform": "linux", "AgentTZ": rotulo,
			})
			return sb.String(), err
		},
		"editar tarefa de banco": func() (string, error) {
			var sb strings.Builder
			err := editDBTmpl.Execute(&sb, map[string]any{
				"Name": "agente", "Base": "", "CSRF": "t", "JobID": "1", "Job": job,
				"AgentTZ": rotulo,
			})
			return sb.String(), err
		},
	}

	for nome, render := range paginas {
		html, err := render()
		if err != nil {
			t.Fatalf("%s: falhou ao renderizar: %v", nome, err)
		}
		if !strings.Contains(html, "America/Sao_Paulo") {
			t.Errorf("%s: o fuso do agente não aparece junto ao agendamento", nome)
		}
		if !strings.Contains(html, "gb-sched-tz") {
			t.Errorf("%s: falta o elemento do fuso no widget de agendamento", nome)
		}
	}

	// A página de criação tem DOIS formulários com agendamento (arquivos e
	// banco); ambos precisam do aviso, não só o primeiro.
	var sb strings.Builder
	if err := homeTmpl.Execute(&sb, map[string]any{
		"Name": "agente", "Base": "", "CSRF": "t", "Jobs": nil,
		"Platform": "linux", "AgentTZ": rotulo,
	}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(sb.String(), "gb-sched-tz\""); n < 2 {
		t.Errorf("esperado o aviso nos dois formulários de criação, encontrado %d", n)
	}
}
