// Package api holds the HTTP handlers shared/exposed by gobackup-central
// (agent registration, polling, and the status dashboard).
package api

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/notify"
	"github.com/cleber/gobackup/internal/webauth"
	"github.com/cleber/gobackup/internal/webui"
)

type Server struct {
	Store    *db.Store
	Notifier *notify.Dispatcher
	Auth     *webauth.Manager
	Mux      *http.ServeMux
}

func NewServer(store *db.Store) *Server {
	s := &Server{Store: store, Notifier: &notify.Dispatcher{Store: store}, Auth: webauth.NewManager(store), Mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.Mux.HandleFunc("POST /api/v1/agents/register", s.handleRegister)
	s.Mux.HandleFunc("POST /api/v1/agents/poll", s.handlePoll)
	s.Mux.HandleFunc("POST /api/v1/agents/{id}/commands", s.Auth.RequireLogin(s.handleEnqueueCommand))
	s.Mux.HandleFunc("POST /api/v1/notification-channels", s.Auth.RequireLogin(s.handleCreateNotificationChannel))
	s.Mux.HandleFunc("GET /", s.Auth.RequireLogin(s.handleDashboard))
	s.Mux.HandleFunc("GET /dashboard", s.Auth.RequireLogin(s.handleHealthDashboard))
	s.Mux.HandleFunc("POST /agents/{id}/delete", s.Auth.RequireLoginAndCSRF(s.handleDeleteAgent))
	s.Mux.HandleFunc("GET /agents/{id}/rename", s.Auth.RequireLogin(s.handleRenameAgentForm))
	s.Mux.HandleFunc("POST /agents/{id}/rename", s.Auth.RequireLoginAndCSRF(s.handleRenameAgentSubmit))
	s.Mux.HandleFunc("GET /notifications", s.Auth.RequireLogin(s.handleNotificationsPage))
	s.Mux.HandleFunc("POST /notifications", s.Auth.RequireLoginAndCSRF(s.handleNotificationsSubmit))
	s.Mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	site := &webui.Site{
		Auth:      s.Auth,
		IsCentral: true,
		GetRunner: func(r *http.Request) (webui.Runner, string, error) {
			agentID, err := uuid.Parse(r.PathValue("id"))
			if err != nil {
				return nil, "", fmt.Errorf("invalid agent id")
			}
			_, name, err := s.Store.AgentTenantAndName(r.Context(), agentID)
			if err != nil {
				return nil, "", fmt.Errorf("agent not found")
			}
			return &webui.CentralRunner{Store: s.Store, AgentID: agentID}, name, nil
		},
		BasePath: func(r *http.Request) string {
			return "/agents/" + r.PathValue("id")
		},
	}
	site.Mount(s.Mux, "/agents/{id}")
}

type registerRequest struct {
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
}

type registerResponse struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpError(w, http.StatusBadRequest, "name is required")
		return
	}

	tenantID, err := s.Store.EnsureDefaultTenant(ctx)
	if err != nil {
		log.Println("register: ensure tenant:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	agentID, token, err := s.Store.RegisterAgent(ctx, tenantID, req.Name, req.Hostname)
	if err != nil {
		log.Println("register: create agent:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, registerResponse{AgentID: agentID.String(), Token: token})
}

type commandResultDTO struct {
	CommandID string         `json:"command_id"`
	Status    string         `json:"status"`
	Result    map[string]any `json:"result"`
}

type jobInfoDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Cron       string `json:"cron"`
	Enabled    bool   `json:"enabled"`
	LastStatus string `json:"last_status"`
}

type runningInfoDTO struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	Percent int    `json:"percent"`
	Message string `json:"message"`
}

type pollRequest struct {
	Status         string             `json:"status"`
	CommandResults []commandResultDTO `json:"command_results"`
	Jobs           []jobInfoDTO       `json:"jobs"`
	Running        []runningInfoDTO   `json:"running"`
}

type pollResponse struct {
	Commands []db.QueuedCommand `json:"commands"`
}

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := bearerToken(r)
	if token == "" {
		httpError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}

	agentID, err := s.Store.AuthenticateAgent(ctx, token)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	var req pollRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // best-effort; body is optional

	if err := s.Store.TouchAgentHeartbeat(ctx, agentID, req.Status); err != nil {
		log.Println("poll: heartbeat:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	jobSnapshot := make([]db.RemoteJobInfo, 0, len(req.Jobs))
	for _, j := range req.Jobs {
		jobSnapshot = append(jobSnapshot, db.RemoteJobInfo{JobID: j.ID, Name: j.Name, Kind: j.Kind, Cron: j.Cron, Enabled: j.Enabled, LastStatus: j.LastStatus})
	}
	if err := s.Store.ReplaceAgentJobsSnapshot(ctx, agentID, jobSnapshot); err != nil {
		log.Println("poll: replace jobs snapshot:", err) // non-fatal: dashboard just shows a stale list
	}

	runningSnapshot := make([]db.RemoteExecutionStatus, 0, len(req.Running))
	for _, s2 := range req.Running {
		runningSnapshot = append(runningSnapshot, db.RemoteExecutionStatus{JobID: s2.JobID, Status: s2.Status, Percent: s2.Percent, Message: s2.Message})
	}
	if err := s.Store.ReplaceAgentExecutionStatus(ctx, agentID, runningSnapshot); err != nil {
		log.Println("poll: replace execution status:", err) // non-fatal: progress bar just shows stale data
	}

	for _, cr := range req.CommandResults {
		cmdID, err := uuid.Parse(cr.CommandID)
		if err != nil {
			continue
		}
		if err := s.Store.RecordCommandResult(ctx, cmdID, cr.Status, cr.Result); err != nil {
			log.Println("poll: record command result:", err)
		}
		if cr.Status == "failed" {
			tenantID, name, err := s.Store.AgentTenantAndName(ctx, agentID)
			if err != nil {
				log.Println("poll: agent tenant lookup:", err)
			} else {
				errMsg, _ := cr.Result["error"].(string)
				s.Notifier.Dispatch(ctx, tenantID, "job_failed",
					fmt.Sprintf("[GoBackup] Falha em comando remoto — %s", name),
					fmt.Sprintf("O servidor %q reportou falha ao executar um comando remoto (command_id=%s).\n\nErro: %s", name, cr.CommandID, errMsg))
			}
		}
	}

	commands, err := s.Store.FetchAndDeliverPendingCommands(ctx, agentID)
	if err != nil {
		log.Println("poll: fetch commands:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, pollResponse{Commands: commands})
}

type enqueueRequest struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

// handleEnqueueCommand lets an operator queue a remote command for an agent.
// This is an internal/admin endpoint used until the web UI exists; it takes
// the agent ID from the URL path: POST /api/v1/agents/{id}/commands
func (s *Server) handleEnqueueCommand(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	var req enqueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		httpError(w, http.StatusBadRequest, "type is required")
		return
	}

	cmdID, err := s.Store.EnqueueCommand(ctx, agentID, req.Type, req.Payload)
	if err != nil {
		log.Println("enqueue command:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"command_id": cmdID.String()})
}

type createNotificationChannelRequest struct {
	Type   string         `json:"type"` // "email" | "telegram"
	Config map[string]any `json:"config"`
	Events []string       `json:"events"`
}

// handleCreateNotificationChannel registers an alert channel on the single
// default tenant used by this MVP. POST /api/v1/notification-channels.
func (s *Server) handleCreateNotificationChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createNotificationChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Type != "email" && req.Type != "telegram" {
		httpError(w, http.StatusBadRequest, "type must be email or telegram")
		return
	}

	tenantID, err := s.Store.EnsureDefaultTenant(ctx)
	if err != nil {
		log.Println("create channel: ensure tenant:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	id, err := s.Store.CreateNotificationChannel(ctx, tenantID, req.Type, req.Config, req.Events)
	if err != nil {
		log.Println("create channel:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"channel_id": id.String()})
}

// gbCentralCSS is the shared modern design system for every Central page
// EXCEPT the health wallboard (/dashboard), which intentionally keeps its
// own bold, big-icon look. Mirrors the tokens/approach used by the shared
// agent admin UI (internal/webui/templates.go) so the whole app feels
// consistent — card-style sections, pill badges, label-above-input forms.
const gbCentralCSS = `
* { box-sizing: border-box; }
:root {
  --gb-bg: #0b0c10; --gb-bg-elev: #15161c; --gb-card: #16171e; --gb-card-2: #1b1c24;
  --gb-border: #262835; --gb-border-soft: #1f2029;
  --gb-text: #e8e8ec; --gb-text-muted: #8a8f9c; --gb-text-faint: #5d6170;
  --gb-accent: #2d6cdf; --gb-accent-hover: #4a84ec;
  --gb-success: #2ecc71; --gb-success-bg: rgba(46,204,113,0.12);
  --gb-danger: #f0544a; --gb-danger-bg: rgba(240,84,74,0.12);
  --gb-warning: #e0a030; --gb-warning-bg: rgba(224,160,48,0.12);
  --gb-radius: 12px; --gb-radius-sm: 8px;
}
body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, Roboto, Helvetica, Arial, sans-serif;
  margin: 0; background: radial-gradient(circle at 15% 0%, #15161d 0%, var(--gb-bg) 55%);
  color: var(--gb-text); -webkit-font-smoothing: antialiased;
}
nav {
  background: rgba(21,22,28,0.85); backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--gb-border-soft); padding: 0.9rem 2rem;
  display: flex; justify-content: space-between; align-items: center;
  position: sticky; top: 0; z-index: 10; margin-bottom: 0 !important;
}
nav > a:first-child { display:flex; align-items:center; gap:0.55rem; font-weight:700; font-size:1.05rem; letter-spacing:-0.01em; color: var(--gb-text); text-decoration:none; }
nav > a:first-child::before { content:""; width:10px; height:10px; border-radius:50%; background: var(--gb-accent); box-shadow: 0 0 10px 1px rgba(45,108,223,0.7); display:inline-block; }
nav a { color: var(--gb-text-muted); text-decoration: none; transition: color .15s ease; }
nav a:hover { color: var(--gb-text); }
form.logout { display:inline; margin-left: 1.25rem; }
#gb-content { padding: 2.25rem 2rem 4rem; max-width: 1100px; margin: 0 auto; }
h1 { font-size: 1.6rem; font-weight: 700; letter-spacing: -0.015em; margin: 0 0 1.5rem; }
h2 { font-size: 1.2rem; font-weight: 600; margin: 2.5rem 0 1rem; }
a { color: #7fb4f5; }
input, select, button, textarea { font-size: 0.92rem; font-family: inherit; }
input[type=text], input[type=password], input[type=number], textarea, select {
  background: var(--gb-bg-elev); color: var(--gb-text); border: 1px solid var(--gb-border);
  border-radius: var(--gb-radius-sm); padding: 0.55rem 0.7rem; width:100%; max-width:300px;
  margin: 0.2rem 0; box-sizing: border-box; transition: border-color .15s ease, box-shadow .15s ease;
}
input:focus, select:focus, textarea:focus { outline: none; border-color: var(--gb-accent); box-shadow: 0 0 0 3px rgba(45,108,223,0.22); }
input::placeholder, textarea::placeholder { color: var(--gb-text-faint); }
form > div, fieldset > div { display:flex; flex-direction:column; align-items:flex-start; gap:0.3rem; margin-bottom: 1.1rem; }
button {
  background: var(--gb-accent); color: #fff; border: none; padding: 0.55rem 1.1rem;
  cursor: pointer; border-radius: var(--gb-radius-sm); font-weight: 500;
  transition: background .15s ease, transform .1s ease;
}
button:hover { background: var(--gb-accent-hover); }
button:active { transform: translateY(1px); }
button.secondary { background: transparent; color: var(--gb-danger); border: 1px solid rgba(240,84,74,0.4); }
button.secondary:hover { background: var(--gb-danger-bg); }
button:disabled, button.secondary:disabled { background: var(--gb-card-2) !important; color: var(--gb-text-faint) !important; border-color: var(--gb-border) !important; cursor: not-allowed; }
fieldset {
  border: 1px solid var(--gb-border); background: linear-gradient(160deg, var(--gb-card) 0%, var(--gb-bg-elev) 100%);
  margin-bottom: 1.75rem; border-radius: var(--gb-radius); padding: 1.6rem 1.75rem; box-shadow: 0 6px 20px rgba(0,0,0,0.25);
  max-width: 520px;
}
legend {
  padding: 0.15rem 0.7rem; color: var(--gb-accent); font-weight: 600; font-size: 0.78rem;
  text-transform: uppercase; letter-spacing: 0.05em; background: rgba(45,108,223,0.12); border-radius: 20px;
}
.gb-card {
  border: 1px solid var(--gb-border); background: linear-gradient(160deg, var(--gb-card) 0%, var(--gb-bg-elev) 100%);
  border-radius: var(--gb-radius); box-shadow: 0 6px 20px rgba(0,0,0,0.25); padding: 0.4rem 0; margin-bottom: 1rem;
  overflow-x: auto;
}
table { border-collapse: separate; border-spacing: 0; width: 100%; margin: 0; }
th, td { padding: 0.8rem 1.2rem; text-align: left; border-bottom: 1px solid var(--gb-border-soft); }
th { color: var(--gb-text-muted); font-size: 0.74rem; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600; border-bottom: 1px solid var(--gb-border); }
tbody tr:hover { background: rgba(255,255,255,0.02); }
tbody tr:last-child td { border-bottom: none; }
.online { color: var(--gb-success); font-weight:600; }
.offline { color: var(--gb-danger); font-weight:600; }
.unknown { color: var(--gb-text-muted); }
.status-success, .status-failed, .status-pending, .status-running, .status-acked, .status-delivered {
  display: inline-block; padding: 0.15rem 0.6rem; border-radius: 20px; font-size: 0.78rem; font-weight: 600; text-decoration: none;
}
a.status-running:hover { filter: brightness(1.15); }
.status-success { color: var(--gb-success); background: var(--gb-success-bg); }
.status-failed { color: var(--gb-danger); background: var(--gb-danger-bg); }
.status-pending, .status-running, .status-acked, .status-delivered { color: var(--gb-warning); background: var(--gb-warning-bg); }
.notice { background: var(--gb-success-bg); border:1px solid rgba(46,204,113,0.35); color:#9ee8b4; padding:0.7rem 1.1rem; border-radius: var(--gb-radius-sm); max-width:480px; }
.error { color: var(--gb-danger); }
small { color: var(--gb-text-muted); }
`

var notificationsTmpl = template.Must(template.New("notifications").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>GoBackup Central — Canais de notificação</title>
<style>` + gbCentralCSS + `</style>
</head>
<body>
<nav><a href="/">GoBackup Central</a>
<div style="display:flex; align-items:center; gap:1.25rem;">
  <a href="/notifications">Canais de notificação</a>
  <a href="/dashboard">Dashboard</a>
  <form class="logout" method="post" action="/logout"><button type="submit">Sair</button></form>
</div>
</nav>
<div id="gb-content">
<h1>Canais de notificação</h1>
{{if .Message}}<p class="notice">{{.Message}}</p>{{end}}
<form method="post" action="/notifications">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <div>Tipo:
    <select name="type">
      <option value="email">E-mail</option>
      <option value="telegram">Telegram</option>
    </select>
  </div>
  <fieldset>
  <legend>E-mail</legend>
  <div>SMTP host: <input type="text" name="smtp_host"></div>
  <div>SMTP porta: <input type="number" name="smtp_port" value="25"></div>
  <div>Usuário SMTP (opcional): <input type="text" name="smtp_user"></div>
  <div>Senha SMTP (opcional): <input type="password" name="smtp_password"></div>
  <div>De: <input type="text" name="from"></div>
  <div>Para: <input type="text" name="to"></div>
  </fieldset>
  <fieldset>
  <legend>Telegram</legend>
  <div>Bot token: <input type="text" name="bot_token"></div>
  <div>Chat ID: <input type="text" name="chat_id"></div>
  </fieldset>
  <button type="submit">Adicionar canal</button>
</form>
<p><a href="/">&larr; voltar ao dashboard</a></p>
</div>
</body>
</html>`))

func (s *Server) handleNotificationsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	notificationsTmpl.Execute(w, map[string]any{"CSRF": s.Auth.CSRFToken(r)})
}

func (s *Server) handleNotificationsSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	r.ParseForm()
	chType := r.FormValue("type")

	var config map[string]any
	switch chType {
	case "email":
		port := 25
		fmt.Sscanf(r.FormValue("smtp_port"), "%d", &port)
		config = map[string]any{
			"smtp_host": r.FormValue("smtp_host"),
			"smtp_port": port,
			"username":  r.FormValue("smtp_user"),
			"password":  r.FormValue("smtp_password"),
			"from":      r.FormValue("from"),
			"to":        r.FormValue("to"),
		}
	case "telegram":
		config = map[string]any{
			"bot_token": r.FormValue("bot_token"),
			"chat_id":   r.FormValue("chat_id"),
		}
	default:
		httpError(w, http.StatusBadRequest, "tipo inválido")
		return
	}

	tenantID, err := s.Store.EnsureDefaultTenant(ctx)
	if err != nil {
		log.Println("notifications: ensure tenant:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := s.Store.CreateNotificationChannel(ctx, tenantID, chType, config, nil); err != nil {
		log.Println("notifications: create:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	notificationsTmpl.Execute(w, map[string]any{"Message": "Canal adicionado com sucesso.", "CSRF": s.Auth.CSRFToken(r)})
}

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"humanizeCron": webui.HumanizeCron,
}).Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="3">
<title>GoBackup Central</title>
<style>` + gbCentralCSS + `
tr.gb-row-offline { color: var(--gb-text-faint); }
tr.gb-row-offline td { color: var(--gb-text-faint); }
.gb-disabled-link { color: var(--gb-text-faint); cursor:not-allowed; pointer-events:none; }
.gb-health-dot { font-size:1.1rem; }
</style>
</head>
<body>
<nav><a href="/">GoBackup Central</a>
<div style="display:flex; align-items:center; gap:1.25rem;">
  <a href="/notifications">Canais de notificação</a>
  <a href="/dashboard">Dashboard</a>
  <form class="logout" method="post" action="/logout"><button type="submit">Sair</button></form>
</div>
</nav>
<div id="gb-content">
<h1>Status dos servidores</h1>
<div class="gb-card">
<table>
<tr><th>Agente</th><th>Hostname</th><th>Status</th><th>Último heartbeat</th><th>Saúde</th><th></th></tr>
{{range .Agents}}
<tr>
  <td>{{.Name}}</td>
  <td>{{.Hostname}}</td>
  <td class="{{.Status}}">{{.Status}}</td>
  <td>{{.LastSeen}}</td>
  <td>{{if .HasFailure}}<span class="gb-health-dot" style="color: var(--gb-danger);" title="alguma tarefa deste agente falhou na última execução">&#9679;</span>{{else}}<span class="gb-health-dot" style="color: var(--gb-success);" title="nenhuma falha nas últimas execuções">&#9679;</span>{{end}}</td>
  <td style="white-space:nowrap;">
    <a href="/agents/{{.ID}}/">administrar</a><span style="display:inline-block; width:2rem;"></span><a href="/agents/{{.ID}}/rename">renomear</a><span style="display:inline-block; width:2rem;"></span><span style="border-left:1px solid var(--gb-border); padding-left:1rem;">
    <form method="post" action="/agents/{{.ID}}/delete" style="display:inline" onsubmit="return confirm('Apagar o agente \'{{.Name}}\' do Central? O agente continua rodando no servidor dele, mas some deste painel e precisa ser registrado de novo (tela Configurações do agente) para reaparecer.');">
      <input type="hidden" name="csrf_token" value="{{$.CSRF}}">
      <button type="submit" class="secondary">Apagar</button>
    </form>
    </span>
  </td>
</tr>
{{else}}
<tr><td colspan="6">nenhum agente cadastrado ainda</td></tr>
{{end}}
</table>
</div>

<h2>Tarefas de todos os servidores</h2>
<div class="gb-card">
<table>
<tr><th>Servidor</th><th>Tarefa</th><th>Tipo</th><th>Agendamento</th><th>Status</th><th>Ação</th></tr>
{{range .Jobs}}
<tr class="{{if not .AgentOnline}}gb-row-offline{{end}}">
  <td>{{.AgentName}}{{if not .AgentOnline}} <small>(offline)</small>{{end}}</td>
  <td>{{.Name}}{{if not .Enabled}} <small>(desabilitada)</small>{{end}}</td>
  <td>{{if eq .Kind "files"}}Arquivos{{else}}Banco de dados{{end}}</td>
  <td>{{if .Cron}}{{humanizeCron .Cron}}{{else}}<small>manual</small>{{end}}</td>
  <td>{{if eq .JobStatus "running"}}<a href="/agents/{{.AgentID}}/jobs/{{.JobID}}/progress" class="status-running">Executando</a>{{else if eq .JobStatus "success"}}<span class="status-success">sucesso</span>{{else if eq .JobStatus "failed"}}<span class="status-failed">falhou</span>{{else}}<small>nunca executada</small>{{end}}</td>
  <td style="white-space:nowrap;">
    {{if .AgentOnline}}
    <a href="/agents/{{.AgentID}}/jobs/{{.JobID}}/edit">Editar</a>
    <form method="post" action="/agents/{{.AgentID}}/jobs/{{.JobID}}/run" style="display:inline-flex; gap:0.5rem; align-items:center; margin-left:0.8rem">
      <input type="hidden" name="csrf_token" value="{{$.CSRF}}">
      {{if eq .Kind "database"}}<input type="password" name="password" placeholder="senha (se necessário)">{{end}}
      <button type="submit">Executar</button>
    </form>
    <span style="display:inline-block; width:1.5rem;"></span><span style="border-left:1px solid var(--gb-border); padding-left:1rem;">
    <form method="post" action="/agents/{{.AgentID}}/jobs/{{.JobID}}/delete" style="display:inline" onsubmit="return confirm('Apagar a tarefa \'{{.Name}}\' do agente \'{{.AgentName}}\'? Isso remove a definição e o histórico de execuções (os arquivos já copiados NÃO são apagados).');">
      <input type="hidden" name="csrf_token" value="{{$.CSRF}}">
      <button type="submit" class="secondary">Apagar</button>
    </form>
    </span>
    {{else}}
    <a class="gb-disabled-link" title="agente offline — ações indisponíveis até reconectar">Editar</a>
    <button type="button" disabled title="agente offline — ações indisponíveis até reconectar" style="margin-left:0.8rem">Executar</button>
    <span style="display:inline-block; width:1.5rem;"></span><span style="border-left:1px solid var(--gb-border); padding-left:1rem;">
    <button type="button" class="secondary" disabled title="agente offline — ações indisponíveis até reconectar">Apagar</button>
    </span>
    {{end}}
  </td>
</tr>
{{else}}
<tr><td colspan="6">nenhuma tarefa criada ainda em nenhum servidor</td></tr>
{{end}}
</table>
</div>
</div>
</body>
</html>`))

type dashboardAgentView struct {
	ID         string
	Name       string
	Hostname   string
	Status     string
	LastSeen   string
	HasFailure bool
}

type dashboardJobView struct {
	AgentID     string
	AgentName   string
	JobID       string
	Name        string
	Kind        string
	Cron        string
	Enabled     bool
	AgentOnline bool
	JobStatus   string
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agents, err := s.Store.ListAgents(ctx)
	if err != nil {
		log.Println("dashboard: list agents:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	health, err := s.Store.GetAgentsHealth(ctx)
	if err != nil {
		log.Println("dashboard: get agents health:", err) // non-fatal: everyone shows green
	}

	views := make([]dashboardAgentView, 0, len(agents))
	for _, a := range agents {
		lastSeen := "nunca"
		if a.LastSeenAt != nil {
			lastSeen = fmt.Sprintf("%s (%s atrás)", a.LastSeenAt.Local().Format("2006-01-02 15:04:05"), time.Since(*a.LastSeenAt).Round(time.Second))
		}
		views = append(views, dashboardAgentView{
			ID:         a.ID.String(),
			Name:       a.Name,
			Hostname:   a.Hostname,
			Status:     a.Status,
			LastSeen:   lastSeen,
			HasFailure: health[a.ID],
		})
	}

	onlineByAgent := map[uuid.UUID]bool{}
	for _, a := range agents {
		onlineByAgent[a.ID] = a.Status == "online"
	}

	jobRows, err := s.Store.ListAllAgentJobs(ctx)
	if err != nil {
		log.Println("dashboard: list all agent jobs:", err) // non-fatal: show the page without the jobs table
	}
	jobViews := make([]dashboardJobView, 0, len(jobRows))
	for _, j := range jobRows {
		jobViews = append(jobViews, dashboardJobView{
			AgentID: j.AgentID.String(), AgentName: j.AgentName, JobID: j.JobID, Name: j.Name, Kind: j.Kind,
			Cron: j.Cron, Enabled: j.Enabled, AgentOnline: onlineByAgent[j.AgentID], JobStatus: j.LastStatus,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTmpl.Execute(w, struct {
		Agents []dashboardAgentView
		Jobs   []dashboardJobView
		CSRF   string
	}{views, jobViews, s.Auth.CSRFToken(r)}); err != nil {
		log.Println("dashboard: render:", err)
	}
}

var healthDashboardTmpl = template.Must(template.New("health-dashboard").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="3">
<title>GoBackup Central — Dashboard</title>
<style>
* { box-sizing: border-box; }
body {
  font-family: -apple-system, "Segoe UI", Roboto, sans-serif;
  background: radial-gradient(circle at 20% 0%, #1a1c24 0%, #0b0c10 55%);
  color: #eee; margin: 0; padding: 2.5rem;
}
nav { margin-bottom: 1.5rem; }
nav a { color:#9ad1ff; text-decoration:none; }
nav a:hover { text-decoration:underline; }
form.logout { display:inline; margin-left: 1rem; }
button { background:#2d6cdf; color:#fff; border:none; padding:0.4rem 1rem; cursor:pointer; border-radius:4px; }
h1 { margin: 0 0 0.3rem; font-weight: 700; letter-spacing: -0.02em; }
.subtitle { color: #8a8f99; margin-bottom: 2rem; font-size: 0.95rem; }
.grid {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(270px, 1fr));
  gap: 1.5rem; max-width: 1300px;
}
.card {
  background: linear-gradient(160deg, #1c1e27 0%, #14151b 100%);
  border: 1px solid #272a33; border-radius: 16px; padding: 1.8rem 1.5rem;
  text-align: center; box-shadow: 0 8px 24px rgba(0,0,0,.45);
  transition: transform .15s ease, border-color .15s ease;
}
.card:hover { transform: translateY(-3px); border-color: #3a3f4d; }
.card h2 { margin: 0 0 1.4rem; font-size: 1.15rem; font-weight: 600; color: #f2f2f2; }
.card .hostname { color:#6d7280; font-size:0.78rem; margin-top:-1.1rem; margin-bottom:1.3rem; }
.indicators { display: flex; justify-content: center; gap: 2.2rem; }
.indicator { display: flex; flex-direction: column; align-items: center; gap: 0.6rem; }
.badge {
  width: 76px; height: 76px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  font-size: 2.1rem; color: #0b0c10;
}
.badge.green { background: radial-gradient(circle at 32% 28%, #7cf79a, #22b24a); box-shadow: 0 0 22px 5px rgba(46,204,113,.5); }
.badge.red   { background: radial-gradient(circle at 32% 28%, #ff9d93, #d9332a); box-shadow: 0 0 22px 5px rgba(244,67,54,.55); }
.badge.pulse { animation: pulse 1.8s infinite; }
@keyframes pulse {
  0%   { box-shadow: 0 0 0 0 rgba(46,204,113,.55); }
  70%  { box-shadow: 0 0 0 18px rgba(46,204,113,0); }
  100% { box-shadow: 0 0 0 0 rgba(46,204,113,0); }
}
.badge.red.pulse {
  animation: pulse-red 1.8s infinite;
}
@keyframes pulse-red {
  0%   { box-shadow: 0 0 0 0 rgba(244,67,54,.55); }
  70%  { box-shadow: 0 0 0 18px rgba(244,67,54,0); }
  100% { box-shadow: 0 0 0 0 rgba(244,67,54,0); }
}
.label { font-size: 0.72rem; color: #9aa0ab; text-transform: uppercase; letter-spacing: 0.06em; }
.sub { font-size: 0.75rem; color: #6d7280; margin-top: 0.15rem; }
.empty { color: #777; }
</style>
</head>
<body>
<nav><a href="/">&larr; GoBackup Central</a> · <a href="/notifications">Canais de notificação</a>
<form class="logout" method="post" action="/logout"><button type="submit">Sair</button></form></nav>
<h1>Dashboard</h1>
<p class="subtitle">Heartbeat e saúde de todos os servidores, em tempo real</p>
<div class="grid">
{{range .Cards}}
<div class="card">
  <h2>{{.Name}}</h2>
  <div class="hostname">{{.Hostname}}</div>
  <div class="indicators">
    <div class="indicator">
      <div class="badge {{if .Online}}green pulse{{else}}red{{end}}">&hearts;</div>
      <div class="label">Heartbeat</div>
      <div class="sub">{{if .Online}}online{{else}}offline{{end}} &middot; {{.LastSeen}}</div>
    </div>
    <div class="indicator">
      <div class="badge {{if .HasFailure}}red{{else}}green{{end}}">{{if .HasFailure}}&#10006;{{else}}&#10004;{{end}}</div>
      <div class="label">Saúde</div>
      <div class="sub">{{if .HasFailure}}falha detectada{{else}}tudo ok{{end}}</div>
    </div>
  </div>
</div>
{{else}}
<p class="empty">nenhum agente cadastrado ainda</p>
{{end}}
</div>
</body>
</html>`))

type healthCardView struct {
	Name       string
	Hostname   string
	Online     bool
	LastSeen   string
	HasFailure bool
}

// handleHealthDashboard renders the "bonito" big-icon dashboard: just
// heartbeat (online/offline) and health (any job failed?) per agent, each
// as a large red/green badge, for an at-a-glance wallboard view.
func (s *Server) handleHealthDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agents, err := s.Store.ListAgents(ctx)
	if err != nil {
		log.Println("health dashboard: list agents:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}
	health, err := s.Store.GetAgentsHealth(ctx)
	if err != nil {
		log.Println("health dashboard: get agents health:", err) // non-fatal: everyone shows green
	}

	cards := make([]healthCardView, 0, len(agents))
	for _, a := range agents {
		lastSeen := "nunca"
		if a.LastSeenAt != nil {
			lastSeen = fmt.Sprintf("%s atrás", time.Since(*a.LastSeenAt).Round(time.Second))
		}
		cards = append(cards, healthCardView{
			Name:       a.Name,
			Hostname:   a.Hostname,
			Online:     a.Status == "online",
			LastSeen:   lastSeen,
			HasFailure: health[a.ID],
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := healthDashboardTmpl.Execute(w, struct {
		Cards []healthCardView
	}{cards}); err != nil {
		log.Println("health dashboard: render:", err)
	}
}

// handleDeleteAgent removes an agent's registration from the Central — it
// does NOT reach into the agent itself (no remote command is sent), so the
// agent keeps running and keeps its own local jobs/Postgres untouched; it
// would just need to be registered again (via its own "Configurações" page)
// to reappear on this dashboard.
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	agentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, "id de agente inválido")
		return
	}
	if err := s.Store.DeleteAgent(r.Context(), agentID); err != nil {
		log.Println("delete agent:", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

var renameAgentTmpl = template.Must(template.New("rename-agent").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>GoBackup Central — Renomear agente</title>
<style>` + gbCentralCSS + `
form { max-width:420px; }
</style>
</head>
<body>
<nav><a href="/">GoBackup Central</a></nav>
<div id="gb-content">
<h1>Renomear agente</h1>
<fieldset>
<form method="post" action="/agents/{{.AgentID}}/rename">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <div>Nome de exibição
    <input type="text" id="name" name="name" value="{{.CurrentName}}" required autofocus>
  </div>
  <button type="submit">Salvar</button>
</form>
</fieldset>
<p><a href="/">&larr; voltar ao dashboard</a></p>
</div>
</body>
</html>`))

// handleRenameAgentForm shows the small rename form, pre-filled with the
// agent's current display name.
func (s *Server) handleRenameAgentForm(w http.ResponseWriter, r *http.Request) {
	agentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, "id de agente inválido")
		return
	}
	name, err := s.Store.GetAgentName(r.Context(), agentID)
	if err != nil {
		log.Println("rename agent form:", err)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := renameAgentTmpl.Execute(w, map[string]any{
		"AgentID":     agentID.String(),
		"CurrentName": name,
		"CSRF":        s.Auth.CSRFToken(r),
	}); err != nil {
		log.Println("rename agent form: render:", err)
	}
}

// handleRenameAgentSubmit applies the new display name. Purely cosmetic —
// it only changes how the agent is labeled across the Central's own UI
// (dashboard, jobs table, health wallboard); nothing is sent to the agent.
func (s *Server) handleRenameAgentSubmit(w http.ResponseWriter, r *http.Request) {
	agentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, "id de agente inválido")
		return
	}
	r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "nome não pode ser vazio", http.StatusBadRequest)
		return
	}
	if err := s.Store.RenameAgent(r.Context(), agentID, name); err != nil {
		log.Println("rename agent:", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
