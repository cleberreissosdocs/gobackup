// Package agentweb implements the agent-only "Settings" screen, where the
// operator points this agent at a Central (URL + CA cert) directly from
// the browser instead of CLI flags. Registering here stores the issued
// token in the agent's local Postgres, and the embedded heartbeat
// (poller.Runner, started once by `gobackup-agent serve`) picks up the new
// settings on its very next tick — no restart needed.
package agentweb

import (
	"html/template"
	"net/http"
	"os"

	"github.com/cleber/gobackup/internal/poller"
	"github.com/cleber/gobackup/internal/webauth"
	"github.com/cleber/gobackup/internal/webui"
)

type SettingsHandler struct {
	Auth  *webauth.Manager
	Heart *poller.Runner
}

func (h *SettingsHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings", h.Auth.RequireLogin(h.handleForm))
	mux.HandleFunc("POST /settings", h.Auth.RequireLoginAndCSRF(h.handleSubmit))
	mux.HandleFunc("POST /settings/unregister", h.Auth.RequireLoginAndCSRF(h.handleUnregister))
}

func (h *SettingsHandler) render(w http.ResponseWriter, r *http.Request, data map[string]any) {
	webui.RenderPage(w, h.Auth, true, r, "Configurações", SettingsTmpl, data)
}

func (h *SettingsHandler) handleForm(w http.ResponseWriter, r *http.Request) {
	status := h.Heart.Status(r.Context())
	h.render(w, r, map[string]any{
		"Status": status,
		"CSRF":   h.Auth.CSRFToken(r),
	})
}

func (h *SettingsHandler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	centralURL := r.FormValue("central_url")
	name := r.FormValue("name")
	caPEM := []byte(r.FormValue("central_ca_pem"))

	var message, errMsg string
	if centralURL == "" || name == "" {
		errMsg = "informe a URL do Central e um nome para este agente"
	} else {
		hostname, _ := os.Hostname()
		res, err := poller.Register(centralURL, name, hostname, caPEM)
		if err != nil {
			errMsg = "falha ao registrar: " + err.Error()
		} else if err := h.Heart.Configure(r.Context(), centralURL, res.Token, caPEM); err != nil {
			errMsg = "registrado, mas falha ao salvar config local: " + err.Error()
		} else {
			message = "Registrado com sucesso (agent_id=" + res.AgentID + "). O heartbeat já está usando esta configuração."
		}
	}

	status := h.Heart.Status(r.Context())
	h.render(w, r, map[string]any{
		"Status":  status,
		"CSRF":    h.Auth.CSRFToken(r),
		"Message": message,
		"Error":   errMsg,
	})
}

func (h *SettingsHandler) handleUnregister(w http.ResponseWriter, r *http.Request) {
	var message, errMsg string
	if err := h.Heart.Unregister(r.Context()); err != nil {
		errMsg = "falha ao desregistrar: " + err.Error()
	} else {
		message = "Desregistrado. Este agente não envia mais heartbeat para nenhum Central. As tarefas e o histórico locais continuam intactos — você pode registrar novamente (neste ou em outro Central) a qualquer momento."
	}
	status := h.Heart.Status(r.Context())
	h.render(w, r, map[string]any{
		"Status":  status,
		"CSRF":    h.Auth.CSRFToken(r),
		"Message": message,
		"Error":   errMsg,
	})
}

var SettingsTmpl = template.Must(template.New("settings").Parse(`
<h1>Configurações</h1>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
{{if .Message}}<p>{{.Message}}</p>{{end}}

<fieldset>
<legend>Status da conexão com o Central</legend>
<p>
  URL configurada: <strong>{{if .Status.CentralURL}}{{.Status.CentralURL}}{{else}}(nenhuma){{end}}</strong><br>
  Status: <strong>{{if .Status.Configured}}configurado{{else}}não configurado{{end}}</strong><br>
  {{if .Status.LastBeatAt}}
  Último heartbeat: {{.Status.LastBeatAt}} —
  {{if .Status.LastBeatOK}}<span class="status-success">OK</span>{{else}}<span class="status-failed">falhou: {{.Status.LastBeatError}}</span>{{end}}
  {{else}}
  Nenhum heartbeat enviado ainda.
  {{end}}
</p>
{{if .Status.Configured}}
<form method="post" action="/settings/unregister" onsubmit="return confirm('Desregistrar este agente do Central? Ele para de enviar heartbeat imediatamente. As tarefas e o histórico locais NÃO são apagados — você pode registrar de novo depois.');">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <button type="submit" class="danger">Desregistrar deste Central</button>
</form>
{{end}}
</fieldset>

<fieldset>
<legend>Apontar para outro servidor Central</legend>
<form method="post" action="/settings">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <div>Nome deste agente (como aparecerá no painel): <input type="text" name="name" required></div>
  <div>URL do Central (ex: https://central.minhaempresa.com:61443): <input type="text" name="central_url" required></div>
  <div>Certificado CA do Central (cole o conteúdo do .pem — deixe em branco se o Central usa um certificado de CA pública confiável):
    <textarea name="central_ca_pem" rows="10" style="width:100%; max-width:560px; background:#1b1b22; color:#eee; border:1px solid #333; font-family:monospace;"></textarea>
  </div>
  <button type="submit">Registrar e conectar</button>
</form>
<p><small>Isso gera um novo token de registro no Central e substitui a configuração atual deste agente.</small></p>
</fieldset>
`))
