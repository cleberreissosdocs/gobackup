// Command gobackup-winagent-tray is the desktop variant of the Windows
// agent: a system-tray icon (red = desconectado, verde = conectado) that
// runs the exact same heartbeat engine as `gobackup-winagent serve`
// (internal/winagentcore), plus a tiny localhost-only web page — opened
// from the tray menu — for registering/unregistering with a Central. No
// terminal window, no remote-accessible web UI: the config page only ever
// listens on 127.0.0.1, never on the network.
package main

import (
	_ "embed"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/getlantern/systray"

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

// lastTick is read by the local web page to show live status without
// re-implementing the heartbeat itself.
var lastTick winagentcore.TickResult

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

	// Same heartbeat engine `serve` uses, just reporting into the tray
	// instead of a terminal.
	go winagentcore.RunLoop(time.Second, func(r winagentcore.TickResult) {
		lastTick = r
		switch {
		case !r.Configured:
			systray.SetIcon(iconRed)
			systray.SetTooltip("GoBackup: não configurado — clique em Configurações")
			mStatus.SetTitle("Status: não configurado")
		case r.Err != nil:
			systray.SetIcon(iconRed)
			systray.SetTooltip("GoBackup: desconectado — " + r.Err.Error())
			mStatus.SetTitle("Status: desconectado")
		case r.Connected:
			systray.SetIcon(iconGreen)
			systray.SetTooltip("GoBackup: conectado")
			mStatus.SetTitle("Status: conectado (" + time.Now().Format("15:04:05") + ")")
		}
	}, stop)

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

func runConfigServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleConfigPage)
	mux.HandleFunc("POST /register", handleRegister)
	mux.HandleFunc("POST /unregister", handleUnregister)
	addr := fmt.Sprintf("127.0.0.1:%d", configPort)
	_ = http.ListenAndServe(addr, mux)
}

const pageTmpl = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<title>GoBackup — Configuração do agente</title>
<style>
  body { font-family: system-ui, sans-serif; background: #0b0d12; color: #e8e8ea; max-width: 560px; margin: 40px auto; padding: 0 16px; }
  h1 { font-size: 1.3rem; }
  label { display: block; margin-top: 14px; font-size: .9rem; color: #aaa; }
  input[type=text] { width: 100%%; box-sizing: border-box; padding: 8px; margin-top: 4px; background: #181b22; border: 1px solid #333; border-radius: 6px; color: #e8e8ea; }
  button { margin-top: 18px; padding: 9px 18px; border: 0; border-radius: 6px; cursor: pointer; font-size: .95rem; }
  .primary { background: #2563eb; color: white; }
  .danger { background: #3a1f1f; color: #ff8080; border: 1px solid #5a2a2a; }
  .status { display: inline-block; padding: 3px 10px; border-radius: 999px; font-size: .85rem; margin-bottom: 10px; }
  .ok { background: #113322; color: #4ade80; }
  .bad { background: #331313; color: #f87171; }
  .hint { color: #888; font-size: .82rem; margin-top: 4px; }
</style>
</head>
<body>
<h1>🐴 GoBackup — Agente Windows</h1>
<p><span class="status %s">%s</span></p>
%s
<form method="post" action="/register">
  <label>Central (URL)</label>
  <input type="text" name="central" value="%s" placeholder="https://central.minhaempresa.com:8443" required>

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
	if cfg.IsConfigured() {
		infoBlock = fmt.Sprintf(`<p>Registrado em <b>%s</b> como <b>%s</b>.</p>`, htmlEscape(cfg.CentralURL), htmlEscape(cfg.Name))
		unregisterBlock = `<form method="post" action="/unregister" onsubmit="return confirm('Desregistrar deste Central? (isso não remove o agente do dashboard do Central, use Apagar lá para isso)');">
  <button type="submit" class="danger">Desregistrar</button>
</form>`
	}

	buttonLabel := "Registrar"
	if cfg.IsConfigured() {
		buttonLabel = "Atualizar registro"
	}

	hostname := cfg.Hostname
	fmt.Fprintf(w, pageTmpl, statusClass, statusText, infoBlock, htmlEscape(cfg.CentralURL), htmlEscape(cfg.Name), htmlEscape(hostname), buttonLabel, unregisterBlock)
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
