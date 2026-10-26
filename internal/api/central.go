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
)

type Server struct {
	Store *db.Store
	Mux   *http.ServeMux
}

func NewServer(store *db.Store) *Server {
	s := &Server{Store: store, Mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.Mux.HandleFunc("POST /api/v1/agents/register", s.handleRegister)
	s.Mux.HandleFunc("POST /api/v1/agents/poll", s.handlePoll)
	s.Mux.HandleFunc("POST /api/v1/agents/{id}/commands", s.handleEnqueueCommand)
	s.Mux.HandleFunc("GET /", s.handleDashboard)
	s.Mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
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

type pollRequest struct {
	Status         string             `json:"status"`
	CommandResults []commandResultDTO `json:"command_results"`
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

	for _, cr := range req.CommandResults {
		cmdID, err := uuid.Parse(cr.CommandID)
		if err != nil {
			continue
		}
		if err := s.Store.RecordCommandResult(ctx, cmdID, cr.Status, cr.Result); err != nil {
			log.Println("poll: record command result:", err)
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

var dashboardTmpl = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="10">
<title>GoBackup Central</title>
<style>
body { font-family: sans-serif; margin: 2rem; background:#111; color:#eee; }
table { border-collapse: collapse; width: 100%; max-width: 900px; }
th, td { border: 1px solid #333; padding: 0.5rem 1rem; text-align: left; }
th { background: #222; }
.online { color: #4caf50; font-weight: bold; }
.offline { color: #f44336; font-weight: bold; }
.unknown { color: #999; }
</style>
</head>
<body>
<h1>GoBackup Central — status dos servidores</h1>
<table>
<tr><th>Agente</th><th>Hostname</th><th>Status</th><th>Último heartbeat</th></tr>
{{range .Agents}}
<tr>
  <td>{{.Name}}</td>
  <td>{{.Hostname}}</td>
  <td class="{{.Status}}">{{.Status}}</td>
  <td>{{.LastSeen}}</td>
</tr>
{{else}}
<tr><td colspan="4">nenhum agente cadastrado ainda</td></tr>
{{end}}
</table>
</body>
</html>`))

type dashboardAgentView struct {
	Name     string
	Hostname string
	Status   string
	LastSeen string
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agents, err := s.Store.ListAgents(ctx)
	if err != nil {
		log.Println("dashboard: list agents:", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	views := make([]dashboardAgentView, 0, len(agents))
	for _, a := range agents {
		lastSeen := "nunca"
		if a.LastSeenAt != nil {
			lastSeen = fmt.Sprintf("%s (%s atrás)", a.LastSeenAt.Local().Format("2006-01-02 15:04:05"), time.Since(*a.LastSeenAt).Round(time.Second))
		}
		views = append(views, dashboardAgentView{
			Name:     a.Name,
			Hostname: a.Hostname,
			Status:   a.Status,
			LastSeen: lastSeen,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTmpl.Execute(w, struct{ Agents []dashboardAgentView }{views}); err != nil {
		log.Println("dashboard: render:", err)
	}
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
