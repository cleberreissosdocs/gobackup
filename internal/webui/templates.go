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
.gb-path-row { display:flex; gap:0.5rem; align-items:center; max-width: 560px; }
.gb-path-row input[type=text] { flex:1; }
button { background:#2d6cdf; color:#fff; border:none; padding:0.5rem 1rem; cursor:pointer; border-radius:4px; }
button:hover { background:#3a7ef0; }
button.secondary { background:#333; }
button.secondary:hover { background:#444; }
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
#gb-picker-modal { display:none; position:fixed; inset:0; background:rgba(0,0,0,0.7); align-items:center; justify-content:center; z-index:1000; }
#gb-picker-box { background:#1b1b22; border:1px solid #333; border-radius:8px; padding:1.5rem; width:90%; max-width:600px; max-height:80vh; display:flex; flex-direction:column; }
#gb-picker-list { overflow-y:auto; flex:1; margin:1rem 0; border:1px solid #333; border-radius:4px; padding:0.3rem; min-height: 200px; }
.gb-picker-item { padding:0.35rem 0.5rem; cursor:pointer; border-radius:4px; }
.gb-picker-item:hover { background:#2a2a33; }
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

<div id="gb-picker-modal">
  <div id="gb-picker-box">
    <h3>Escolher diretório ou arquivo</h3>
    <div class="gb-path-row">
      <input type="text" id="gb-picker-path">
      <button type="button" onclick="gbGoToTyped()">Ir</button>
    </div>
    <div id="gb-picker-list"></div>
    <div style="display:flex; justify-content:flex-end; gap:0.5rem;">
      <button type="button" class="secondary" onclick="gbClosePicker()">Cancelar</button>
      <button type="button" onclick="gbSelectCurrent()">Usar esta pasta</button>
    </div>
  </div>
</div>
<script>
var gbPickerTarget = null, gbPickerBase = "";
function gbOpenPicker(inputId, base) {
  gbPickerTarget = document.getElementById(inputId);
  gbPickerBase = base;
  document.getElementById('gb-picker-modal').style.display = 'flex';
  gbLoadDir(gbPickerTarget.value || '/');
}
function gbClosePicker() {
  document.getElementById('gb-picker-modal').style.display = 'none';
}
function gbGoToTyped() {
  gbLoadDir(document.getElementById('gb-picker-path').value);
}
function gbSelectCurrent() {
  gbPickerTarget.value = document.getElementById('gb-picker-path').value;
  gbClosePicker();
}
function gbLoadDir(path) {
  fetch(gbPickerBase + '/browse.json?path=' + encodeURIComponent(path), {credentials: 'same-origin'})
    .then(function(r) { return r.json(); })
    .then(function(data) {
      document.getElementById('gb-picker-path').value = data.path;
      var list = document.getElementById('gb-picker-list');
      list.innerHTML = '';
      if (data.error) {
        var errDiv = document.createElement('div');
        errDiv.className = 'error';
        errDiv.textContent = data.error;
        list.appendChild(errDiv);
        return;
      }
      var up = document.createElement('div');
      up.className = 'gb-picker-item dir';
      up.textContent = '.. (subir um nível)';
      up.onclick = function() { gbLoadDir(data.path.replace(/\/+$/, '') + '/..'); };
      list.appendChild(up);
      (data.entries || []).forEach(function(e) {
        var item = document.createElement('div');
        item.className = 'gb-picker-item' + (e.is_dir ? ' dir' : '');
        item.textContent = (e.is_dir ? '\uD83D\uDCC1 ' : '\uD83D\uDCC4 ') + e.name;
        item.onclick = function() {
          var base = data.path.replace(/\/+$/, '');
          var newPath = (base === '' ? '' : base) + '/' + e.name;
          if (e.is_dir) {
            gbLoadDir(newPath);
          } else {
            gbPickerTarget.value = newPath;
            gbClosePicker();
          }
        };
        list.appendChild(item);
      });
    })
    .catch(function(err) {
      var list = document.getElementById('gb-picker-list');
      list.innerHTML = '<div class="error">erro ao carregar: ' + err + '</div>';
    });
}
</script>
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
  <div>Origem:
    <div class="gb-path-row">
      <input type="text" id="file_source" name="source" required>
      <button type="button" class="secondary" onclick="gbOpenPicker('file_source','{{.Base}}')">Escolher diretório…</button>
    </div>
  </div>
  <div>Destino (caminho):
    <div class="gb-path-row">
      <input type="text" id="file_dest" name="dest" required>
      <button type="button" class="secondary" onclick="gbOpenPicker('file_dest','{{.Base}}')">Escolher diretório…</button>
    </div>
  </div>
  <div>Tipo de backup:
    <select name="backup_type">
      <option value="full">Completo</option>
      <option value="incremental">Incremental</option>
      <option value="differential">Diferencial</option>
    </select>
  </div>
  <div>Baseline (--link-dest, p/ incremental/diferencial):
    <div class="gb-path-row">
      <input type="text" id="file_linkdest" name="link_dest">
      <button type="button" class="secondary" onclick="gbOpenPicker('file_linkdest','{{.Base}}')">Escolher diretório…</button>
    </div>
  </div>
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
  <div>SSH chave privada (caminho no agente):
    <div class="gb-path-row">
      <input type="text" id="file_sshkey" name="ssh_key">
      <button type="button" class="secondary" onclick="gbOpenPicker('file_sshkey','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
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
  <div>Caminho SQLite (só para engine=sqlite):
    <div class="gb-path-row">
      <input type="text" id="db_sqlitepath" name="sqlite_path">
      <button type="button" class="secondary" onclick="gbOpenPicker('db_sqlitepath','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
  <div>Compressão:
    <select name="compression">
      <option value="gzip">gzip</option>
      <option value="zip">zip</option>
      <option value="none">nenhuma</option>
    </select>
  </div>
  <div>Destino (diretório):
    <div class="gb-path-row">
      <input type="text" id="db_dest" name="dest_path" required>
      <button type="button" class="secondary" onclick="gbOpenPicker('db_dest','{{.Base}}')">Escolher diretório…</button>
    </div>
  </div>
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
  <div>SSH chave privada (caminho no agente):
    <div class="gb-path-row">
      <input type="text" id="db_sshkey" name="ssh_key">
      <button type="button" class="secondary" onclick="gbOpenPicker('db_sshkey','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
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
