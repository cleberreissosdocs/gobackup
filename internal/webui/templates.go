package webui

import "html/template"

var funcs = template.FuncMap{
	"fmtTime": func(t interface{}) string {
		type timeLike interface{ Format(string) string }
		if tl, ok := t.(timeLike); ok {
			return tl.Format("2006-01-02 15:04:05")
		}
		return ""
	},
}

var layoutTmpl = template.Must(template.New("layout").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<style>
body { font-family: sans-serif; margin: 0; background:#0f0f12; color:#e8e8e8; }
header { background:#1b1b22; padding: 1rem 2rem; display:flex; justify-content:space-between; align-items:center; }
header a { color:#9ad1ff; text-decoration:none; margin-right:1rem; }
main { padding: 2rem; max-width: 960px; }
input, select, button { font-size: 1rem; padding: 0.4rem; margin: 0.2rem 0; }
input[type=text], input[type=password], input[type=number] { width: 100%; max-width: 420px; background:#1b1b22; color:#eee; border:1px solid #333; }
button { background:#2d6cdf; color:#fff; border:none; padding:0.5rem 1rem; cursor:pointer; border-radius:4px; }
button:hover { background:#3a7ef0; }
table { border-collapse: collapse; width: 100%; margin: 1rem 0; }
th, td { border: 1px solid #333; padding: 0.5rem 0.8rem; text-align: left; }
th { background:#1b1b22; }
fieldset { border: 1px solid #333; margin-bottom: 1.5rem; border-radius: 6px; }
legend { padding: 0 0.5rem; color:#9ad1ff; }
.status-success { color:#4caf50; font-weight:bold; }
.status-failed { color:#f44336; font-weight:bold; }
.status-pending, .status-running, .status-acked, .status-delivered { color:#ffb300; font-weight:bold; }
.error { color:#f44336; }
.dir { color:#9ad1ff; }
small { color:#999; }
</style>
</head>
<body>
<header>
  <div><a href="/">GoBackup</a></div>
  <div>
    {{if .User}}{{.User}} — <form method="post" action="/logout" style="display:inline"><button type="submit">Sair</button></form>{{end}}
  </div>
</header>
<main>
{{.Body}}
</main>
</body>
</html>`))

var loginTmpl = template.Must(template.New("login").Parse(`
<h1>Entrar</h1>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<form method="post" action="/login">
  <div>E-mail: <input type="text" name="email" required></div>
  <div>Senha: <input type="password" name="password" required></div>
  <button type="submit">Entrar</button>
</form>
`))

var homeTmpl = template.Must(template.New("home").Funcs(funcs).Parse(`
<h1>{{.Name}}</h1>

<fieldset>
<legend>Explorar diretórios</legend>
<form method="get" action="{{.Base}}/browse">
  <input type="text" name="path" placeholder="/caminho/no/servidor" required>
  <button type="submit">Listar</button>
</form>
</fieldset>

<fieldset>
<legend>Nova tarefa de arquivos</legend>
<form method="post" action="{{.Base}}/jobs/file">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <div>Nome: <input type="text" name="name" required></div>
  <div>Origem: <input type="text" name="source" required></div>
  <div>Destino (caminho): <input type="text" name="dest" required></div>
  <div>Tipo de backup:
    <select name="backup_type">
      <option value="full">Completo</option>
      <option value="incremental">Incremental</option>
      <option value="differential">Diferencial</option>
    </select>
  </div>
  <div>Baseline (--link-dest, p/ incremental/diferencial): <input type="text" name="link_dest"></div>
  <div>Tipo de destino:
    <select name="dest_type">
      <option value="local">Local</option>
      <option value="nas">NAS (montado)</option>
      <option value="ssh">SSH remoto</option>
    </select>
  </div>
  <div>SSH host: <input type="text" name="ssh_host"></div>
  <div>SSH porta: <input type="number" name="ssh_port" value="22"></div>
  <div>SSH usuário: <input type="text" name="ssh_user"></div>
  <div>SSH chave privada (caminho no agente): <input type="text" name="ssh_key"></div>
  <button type="submit">Criar e executar agora</button>
</form>
</fieldset>

<fieldset>
<legend>Nova tarefa de banco de dados</legend>
<form method="post" action="{{.Base}}/jobs/db">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <div>Nome: <input type="text" name="name" required></div>
  <div>Engine:
    <select name="engine">
      <option value="postgresql">PostgreSQL</option>
      <option value="mysql">MySQL</option>
      <option value="mariadb">MariaDB</option>
      <option value="sqlite">SQLite</option>
    </select>
  </div>
  <div>Host: <input type="text" name="host"></div>
  <div>Porta: <input type="number" name="port"></div>
  <div>Usuário: <input type="text" name="username"></div>
  <div>Senha: <input type="password" name="password"></div>
  <div>Banco específico: <input type="text" name="database"></div>
  <div><label><input type="checkbox" name="all_databases"> Todos os bancos (ignora o campo acima)</label></div>
  <div>Caminho SQLite (só para engine=sqlite): <input type="text" name="sqlite_path"></div>
  <div>Compressão:
    <select name="compression">
      <option value="gzip">gzip</option>
      <option value="zip">zip</option>
      <option value="none">nenhuma</option>
    </select>
  </div>
  <div>Destino (diretório): <input type="text" name="dest_path" required></div>
  <div>Tipo de destino:
    <select name="dest_type">
      <option value="local">Local</option>
      <option value="nas">NAS (montado)</option>
      <option value="ssh">SSH remoto</option>
    </select>
  </div>
  <div>SSH host: <input type="text" name="ssh_host"></div>
  <div>SSH porta: <input type="number" name="ssh_port" value="22"></div>
  <div>SSH usuário: <input type="text" name="ssh_user"></div>
  <div>SSH chave privada (caminho no agente): <input type="text" name="ssh_key"></div>
  <button type="submit">Criar e executar agora</button>
</form>
</fieldset>

<fieldset>
<legend>Atividade recente</legend>
<table>
<tr><th>Quando</th><th>Descrição</th><th>Status</th></tr>
{{range .Activity}}
<tr>
  <td>{{fmtTime .When}}</td>
  <td>{{.Description}}</td>
  <td class="status-{{.Status}}">{{.Status}}</td>
</tr>
{{else}}
<tr><td colspan="3">nenhuma atividade ainda</td></tr>
{{end}}
</table>
</fieldset>
`))

var browseTmpl = template.Must(template.New("browse").Parse(`
<h1>Explorar: {{.Path}}</h1>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<table>
<tr><th>Nome</th><th>Tipo</th></tr>
{{range .Entries}}
<tr><td class="{{if .IsDir}}dir{{end}}">{{.Name}}</td><td>{{if .IsDir}}diretório{{else}}arquivo{{end}}</td></tr>
{{else}}
<tr><td colspan="2">vazio ou sem acesso</td></tr>
{{end}}
</table>
<p><a href="{{.Base}}/">&larr; voltar</a></p>
`))

var outcomeTmpl = template.Must(template.New("outcome").Parse(`
<h1>Resultado: {{.JobName}}</h1>
<p class="status-{{.Outcome.Status}}">{{.Outcome.Status}}</p>
<p>{{.Outcome.Message}}</p>
<p><a href="{{.Base}}/">&larr; voltar</a></p>
`))
