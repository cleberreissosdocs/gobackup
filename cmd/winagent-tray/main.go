// Command gobackup-winagent-tray is the desktop variant of the Windows
// agent: a system-tray icon (red = desconectado, verde = conectado) that
// runs the exact same heartbeat engine as `gobackup-winagent serve`
// (internal/winagentcore), plus a tiny localhost-only web page — opened
// from the tray menu — for registering/unregistering with a Central. No
// terminal window, no remote-accessible web UI: the config page only ever
// listens on 127.0.0.1, never on the network.
//
// The config page is password-protected (default admin/admin on a fresh
// install, changeable from the page itself) even though it's loopback-only
// — it still guards against another local user/process on a shared or
// remote-desktop machine silently re-pointing the agent at a different
// Central.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/getlantern/systray"

	"github.com/cleber/gobackup/internal/webui/assets"
	"github.com/cleber/gobackup/internal/winagentcore"
)

//go:embed icons/red.ico
var iconRed []byte

//go:embed icons/green.ico
var iconGreen []byte

//go:embed icons/yellow.ico
var iconYellow []byte

// configPort is fixed (not random) so the tray menu and a user manually
// typing the address into a browser always agree on where the page lives.
const configPort = 47890
const configURL = "http://127.0.0.1:47890/"

const sessionCookie = "gb_session"
const sessionTTL = 12 * time.Hour

// lastTick is read by the local web page to show live status without
// re-implementing the heartbeat itself.
var lastTick winagentcore.TickResult

type session struct {
	csrf      string
	expiresAt time.Time
}

var (
	sessMu   sync.Mutex
	sessions = map[string]session{}
)

func main() {
	if already, err := singleInstanceGuard(); err == nil && already {
		// Another instance is already running — just exit quietly instead
		// of fighting it over the config port and job store.
		return
	}
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(iconYellow)
	systray.SetTitle("")
	systray.SetTooltip("GoBackup: iniciando...")

	mConfig := systray.AddMenuItem("Configurações...", "Abrir a página de configuração")
	systray.AddSeparator()
	mStatus := systray.AddMenuItem("Status: iniciando...", "")
	mStatus.Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Sair", "Encerra o agente GoBackup")

	stop := make(chan struct{})

	// Local-only config page — never bound to anything but loopback.
	go runConfigServer()

	// The ACTUAL heartbeat/backup loop now runs in the Windows Service
	// (see cmd/winagent's service-run), so it keeps working with nobody
	// logged in — that's the whole point. The tray only ever DISPLAYS
	// status here; it must never also run winagentcore.RunLoop itself,
	// or two independent pollers would both execute whatever commands
	// the Central sends (duplicate/conflicting backup runs).
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			cfg, _ := winagentcore.LoadConfig()
			snap, fresh := winagentcore.LoadStatus()
			switch {
			case !cfg.IsConfigured():
				lastTick = winagentcore.TickResult{Configured: false}
				systray.SetIcon(iconRed)
				systray.SetTooltip("GoBackup: não configurado — clique em Configurações")
				mStatus.SetTitle("Status: não configurado")
			case !fresh:
				lastTick = winagentcore.TickResult{Configured: true}
				systray.SetIcon(iconRed)
				systray.SetTooltip("GoBackup: serviço não está rodando — veja 'service-install'")
				mStatus.SetTitle("Status: serviço não está rodando")
			case snap.Err != nil:
				lastTick = snap.TickResult
				systray.SetIcon(iconRed)
				systray.SetTooltip("GoBackup: desconectado — " + snap.Err.Error())
				mStatus.SetTitle("Status: desconectado")
			case snap.Connected:
				lastTick = snap.TickResult
				systray.SetIcon(iconGreen)
				systray.SetTooltip("GoBackup: conectado")
				mStatus.SetTitle("Status: conectado (" + snap.UpdatedAt.Format("15:04:05") + ")")
			}
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()

	go func() {
		for {
			select {
			case <-mConfig.ClickedCh:
				openBrowser(configURL)
			case <-mQuit.ClickedCh:
				close(stop)
				systray.Quit()
				return
			}
		}
	}()
}

func onExit() {}

func openBrowser(url string) {
	// "start" is a cmd.exe builtin, not a real executable — must go
	// through cmd /c, and the empty "" first arg is the (unused) window
	// title start.exe expects before the URL.
	_ = exec.Command("cmd", "/c", "start", "", url).Start()
}

// ---- auth: a single local password, session cookie + CSRF token ----

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func startSession(w http.ResponseWriter) {
	token := newToken()
	csrf := newToken()
	sessMu.Lock()
	sessions[token] = session{csrf: csrf, expiresAt: time.Now().Add(sessionTTL)}
	sessMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func currentSession(r *http.Request) (session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return session{}, false
	}
	sessMu.Lock()
	defer sessMu.Unlock()
	s, ok := sessions[c.Value]
	if !ok || time.Now().After(s.expiresAt) {
		return session{}, false
	}
	return s, true
}

func endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		sessMu.Lock()
		delete(sessions, c.Value)
		sessMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
}

func csrfOK(r *http.Request) bool {
	s, ok := currentSession(r)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.FormValue("csrf_token")), []byte(s.csrf)) == 1
}

func requireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := currentSession(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func requireLoginAndCSRF(next http.HandlerFunc) http.HandlerFunc {
	return requireLogin(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if !csrfOK(r) {
			http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

func runConfigServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /logo.png", handleLogo)
	mux.HandleFunc("GET /login", handleLoginPage)
	mux.HandleFunc("POST /login", handleLoginSubmit)
	mux.HandleFunc("POST /logout", handleLogout)
	mux.HandleFunc("GET /change-password", requireLogin(handleChangePasswordPage))
	mux.HandleFunc("POST /change-password", requireLoginAndCSRF(handleChangePasswordSubmit))
	mux.HandleFunc("GET /", requireLogin(handleConfigPage))
	mux.HandleFunc("POST /register", requireLoginAndCSRF(handleRegister))
	mux.HandleFunc("POST /unregister", requireLoginAndCSRF(handleUnregister))
	addr := fmt.Sprintf("127.0.0.1:%d", configPort)
	_ = http.ListenAndServe(addr, mux)
}

func handleLogo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Write(assets.LogoPNG)
}

const cssBlock = `
  body { font-family: system-ui, sans-serif; background: #0b0d12; color: #e8e8ea; max-width: 560px; margin: 40px auto; padding: 0 16px; }
  h1 { font-size: 1.3rem; display:flex; align-items:center; gap:0.5rem; }
  h1 img { width:24px; height:24px; }
  label { display: block; margin-top: 14px; font-size: .9rem; color: #aaa; }
  input[type=text], input[type=password] { width: 100%%; box-sizing: border-box; padding: 8px; margin-top: 4px; background: #181b22; border: 1px solid #333; border-radius: 6px; color: #e8e8ea; }
  button { margin-top: 18px; padding: 9px 18px; border: 0; border-radius: 6px; cursor: pointer; font-size: .95rem; }
  .primary { background: #2563eb; color: white; }
  .secondary { background: #262b35; color: #e8e8ea; }
  .danger { background: #3a1f1f; color: #ff8080; border: 1px solid #5a2a2a; }
  .status { display: inline-block; padding: 3px 10px; border-radius: 999px; font-size: .85rem; margin-bottom: 10px; }
  .ok { background: #113322; color: #4ade80; }
  .bad { background: #331313; color: #f87171; }
  .error { background: #331313; color: #f87171; padding: 8px 12px; border-radius: 6px; }
  .hint { color: #888; font-size: .82rem; margin-top: 4px; }
  .gb-login-wrap { display:flex; flex-direction:column; align-items:center; margin-top:60px; }
  .gb-login-logo { width:72px; height:72px; filter: drop-shadow(0 0 18px rgba(45,108,223,.45)); }
  .gb-nav { display:flex; justify-content:space-between; align-items:center; }
  .gb-nav a { color:#9ad1ff; text-decoration:none; font-size:.85rem; }
`

const loginPageTmpl = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<title>GoBackup — Entrar</title>
<style>` + cssBlock + `</style>
</head>
<body>
<div class="gb-login-wrap">
  <img src="/logo.png" class="gb-login-logo" alt="GoBackup">
  <h1 style="margin-top:0.75rem;">GoBackup</h1>
  <p style="color:#aaa; margin-top:-0.5rem;">Agente Windows</p>
  %s
  <form method="post" action="/login" style="width:100%%; max-width:320px;">
    <label>Senha</label>
    <input type="password" name="password" required autofocus>
    <button type="submit" class="primary" style="width:100%%;">Entrar</button>
  </form>
</div>
</body>
</html>`

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	errBlock := ""
	if r.URL.Query().Get("err") == "1" {
		errBlock = `<p class="error">Senha incorreta.</p>`
	}
	fmt.Fprintf(w, loginPageTmpl, errBlock)
}

func handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	cfg, _ := winagentcore.LoadConfig()
	if !cfg.CheckPassword(r.FormValue("password")) {
		http.Redirect(w, r, "/login?err=1", http.StatusSeeOther)
		return
	}
	startSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	endSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

const changePasswordTmpl = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<title>GoBackup — Trocar senha</title>
<style>` + cssBlock + `</style>
</head>
<body>
<p class="gb-nav"><a href="/">&larr; Voltar</a><form method="post" action="/logout" style="display:inline"><button type="submit" class="secondary" style="margin:0; padding:4px 10px; font-size:.8rem;">Sair</button></form></p>
<h1><img src="/logo.png" alt="">GoBackup — Trocar senha</h1>
%s
<form method="post" action="/change-password">
  <input type="hidden" name="csrf_token" value="%s">
  <label>Senha atual</label>
  <input type="password" name="current_password" required>
  <label>Nova senha</label>
  <input type="password" name="new_password" required minlength="4">
  <label>Confirmar nova senha</label>
  <input type="password" name="confirm_password" required minlength="4">
  <button type="submit" class="primary">Salvar nova senha</button>
</form>
</body>
</html>`

func handleChangePasswordPage(w http.ResponseWriter, r *http.Request) {
	s, _ := currentSession(r)
	msgBlock := ""
	switch r.URL.Query().Get("msg") {
	case "ok":
		msgBlock = `<p class="status ok">Senha alterada com sucesso.</p>`
	case "mismatch":
		msgBlock = `<p class="error">A confirmação não bate com a nova senha.</p>`
	case "wrong":
		msgBlock = `<p class="error">Senha atual incorreta.</p>`
	}
	fmt.Fprintf(w, changePasswordTmpl, msgBlock, s.csrf)
}

func handleChangePasswordSubmit(w http.ResponseWriter, r *http.Request) {
	cfg, _ := winagentcore.LoadConfig()
	current := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")

	if !cfg.CheckPassword(current) {
		http.Redirect(w, r, "/change-password?msg=wrong", http.StatusSeeOther)
		return
	}
	if newPw != confirm {
		http.Redirect(w, r, "/change-password?msg=mismatch", http.StatusSeeOther)
		return
	}
	if err := winagentcore.SetPassword(newPw); err != nil {
		http.Error(w, "Falha ao salvar a nova senha: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/change-password?msg=ok", http.StatusSeeOther)
}

const pageTmpl = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<title>GoBackup — Configuração do agente</title>
<style>` + cssBlock + `</style>
</head>
<body>
<p class="gb-nav"><a href="/change-password">Trocar senha</a><form method="post" action="/logout" style="display:inline"><button type="submit" class="secondary" style="margin:0; padding:4px 10px; font-size:.8rem;">Sair</button></form></p>
<h1><img src="/logo.png" alt="">GoBackup — Agente Windows</h1>
<p><span class="status %s">%s</span></p>
%s
<form method="post" action="/register">
  <input type="hidden" name="csrf_token" value="%s">
  <label>Central (URL)</label>
  <input type="text" name="central" value="%s" placeholder="https://central.minhaempresa.com:61443" required>

  <label>Nome deste servidor</label>
  <input type="text" name="name" value="%s" placeholder="%s (hostname atual)">

  <label>Certificado CA (caminho do arquivo, opcional)</label>
  <input type="text" name="ca" placeholder="C:\GoBackup\central-cert.pem">
  <div class="hint">Deixe em branco se o Central tiver certificado público válido (não autoassinado).</div>

  <button type="submit" class="primary">%s</button>
</form>
%s
</body>
</html>`

func handleConfigPage(w http.ResponseWriter, r *http.Request) {
	cfg, _ := winagentcore.LoadConfig()
	statusClass, statusText := "bad", "não configurado"
	if cfg.IsConfigured() {
		if lastTick.Connected {
			statusClass, statusText = "ok", "conectado"
		} else {
			statusClass, statusText = "bad", "desconectado"
		}
	}

	var infoBlock, unregisterBlock string
	s, _ := currentSession(r)
	if cfg.IsConfigured() {
		infoBlock = fmt.Sprintf(`<p>Registrado em <b>%s</b> como <b>%s</b>.</p>`, htmlEscape(cfg.CentralURL), htmlEscape(cfg.Name))
		unregisterBlock = fmt.Sprintf(`<form method="post" action="/unregister" onsubmit="return confirm('Desregistrar deste Central? (isso não remove o agente do dashboard do Central, use Apagar lá para isso)');">
  <input type="hidden" name="csrf_token" value="%s">
  <button type="submit" class="danger">Desregistrar</button>
</form>`, s.csrf)
	}

	buttonLabel := "Registrar"
	if cfg.IsConfigured() {
		buttonLabel = "Atualizar registro"
	}

	hostname := cfg.Hostname
	fmt.Fprintf(w, pageTmpl, statusClass, statusText, infoBlock, s.csrf, htmlEscape(cfg.CentralURL), htmlEscape(cfg.Name), htmlEscape(hostname), buttonLabel, unregisterBlock)
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	central := r.FormValue("central")
	name := r.FormValue("name")
	ca := r.FormValue("ca")
	if central == "" {
		http.Error(w, "Central é obrigatório", http.StatusBadRequest)
		return
	}
	if _, err := winagentcore.RegisterAndSave(central, ca, name); err != nil {
		http.Error(w, "Falha ao registrar: "+err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleUnregister(w http.ResponseWriter, r *http.Request) {
	_ = winagentcore.Unregister()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func htmlEscape(s string) string {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		switch c {
		case '&':
			out = append(out, []rune("&amp;")...)
		case '<':
			out = append(out, []rune("&lt;")...)
		case '>':
			out = append(out, []rune("&gt;")...)
		case '"':
			out = append(out, []rune("&quot;")...)
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
