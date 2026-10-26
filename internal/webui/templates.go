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
    {{if .User}}{{if .ShowSettings}}<a href="/settings">Configurações</a>{{end}}{{.User}} — <form method="post" action="/logout" style="display:inline"><button type="submit">Sair</button></form>{{end}}
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
  <div>Agendamento (cron, opcional — ex: "0 3 * * *" = todo dia às 3h; deixe em branco para só rodar manualmente):
    <input type="text" name="cron" placeholder="minuto hora dia mês dia-da-semana">
  </div>
  <button type="submit">Criar</button>
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
  <div>Agendamento (cron, opcional — lembre-se: se o banco exige senha, um backup agendado vai falhar porque a senha nunca fica salva; agende só se o banco não precisar de autenticação, ou rode manualmente):
    <input type="text" name="cron" placeholder="minuto hora dia mês dia-da-semana">
  </div>
  <button type="submit">Criar</button>
</form>
</fieldset>

<fieldset>
<legend>Tarefas</legend>
<table>
<tr><th>Nome</th><th>Tipo</th><th>Agendamento</th><th>Ação</th></tr>
{{range .Jobs}}
<tr>
  <td>{{.Name}}{{if not .Enabled}} <small>(desabilitada)</small>{{end}}</td>
  <td>{{if eq .Kind "files"}}Arquivos{{else}}Banco de dados{{end}}</td>
  <td>{{if .Cron}}{{.Cron}}{{else}}<small>manual</small>{{end}}</td>
  <td>
    <a href="{{$.Base}}/jobs/{{.ID}}/edit">Editar</a>
    <form method="post" action="{{$.Base}}/jobs/{{.ID}}/run" style="display:inline-flex; gap:0.5rem; align-items:center; margin-left:0.8rem">
      <input type="hidden" name="csrf_token" value="{{$.CSRF}}">
      {{if eq .Kind "database"}}<input type="password" name="password" placeholder="senha (se necessário)">{{end}}
      <button type="submit">Executar</button>
    </form>
  </td>
</tr>
{{else}}
<tr><td colspan="4">nenhuma tarefa criada ainda</td></tr>
{{end}}
</table>
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

var editFileTmpl = template.Must(template.New("edit-file").Parse(`
<h1>Editar tarefa de arquivos: {{.Job.Name}}</h1>
<form method="post" action="{{.Base}}/jobs/{{.JobID}}/update">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <input type="hidden" name="kind" value="files">
  <div>Nome: <input type="text" name="name" value="{{.Job.Name}}" required></div>
  <div>Origem:
    <div class="gb-path-row">
      <input type="text" id="efile_source" name="source" value="{{.Job.Source}}" required>
      <button type="button" class="secondary" onclick="gbOpenPicker('efile_source','{{.Base}}')">Escolher diretório…</button>
    </div>
  </div>
  <div>Destino (caminho):
    <div class="gb-path-row">
      <input type="text" id="efile_dest" name="dest" value="{{.Job.Dest}}" required>
      <button type="button" class="secondary" onclick="gbOpenPicker('efile_dest','{{.Base}}')">Escolher diretório…</button>
    </div>
  </div>
  <div>Tipo de backup:
    <select name="backup_type">
      <option value="full" {{if eq .Job.BackupType "full"}}selected{{end}}>Completo</option>
      <option value="incremental" {{if eq .Job.BackupType "incremental"}}selected{{end}}>Incremental</option>
      <option value="differential" {{if eq .Job.BackupType "differential"}}selected{{end}}>Diferencial</option>
    </select>
  </div>
  <div>Baseline (--link-dest, p/ incremental/diferencial):
    <div class="gb-path-row">
      <input type="text" id="efile_linkdest" name="link_dest" value="{{.Job.LinkDest}}">
      <button type="button" class="secondary" onclick="gbOpenPicker('efile_linkdest','{{.Base}}')">Escolher diretório…</button>
    </div>
  </div>
  <div>Tipo de destino:
    <select name="dest_type">
      <option value="local" {{if eq .Job.DestType "local"}}selected{{end}}>Local</option>
      <option value="nas" {{if eq .Job.DestType "nas"}}selected{{end}}>NAS (montado)</option>
      <option value="ssh" {{if eq .Job.DestType "ssh"}}selected{{end}}>SSH remoto</option>
    </select>
  </div>
  <div>SSH host: <input type="text" name="ssh_host" value="{{.Job.SSHHost}}"></div>
  <div>SSH porta: <input type="number" name="ssh_port" value="{{.Job.SSHPort}}"></div>
  <div>SSH usuário: <input type="text" name="ssh_user" value="{{.Job.SSHUser}}"></div>
  <div>SSH chave privada (caminho no agente):
    <div class="gb-path-row">
      <input type="text" id="efile_sshkey" name="ssh_key" value="{{.Job.SSHKey}}">
      <button type="button" class="secondary" onclick="gbOpenPicker('efile_sshkey','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
  <div>Agendamento (cron, opcional):
    <input type="text" name="cron" value="{{.Job.Cron}}" placeholder="minuto hora dia mês dia-da-semana">
  </div>
  <div><label><input type="checkbox" name="enabled" {{if .Job.Enabled}}checked{{end}}> Habilitada (desmarcar pausa o agendamento, sem apagar a tarefa)</label></div>
  <button type="submit">Salvar</button>
</form>
<p><a href="{{.Base}}/">&larr; voltar</a></p>
`))

var editDBTmpl = template.Must(template.New("edit-db").Parse(`
<h1>Editar tarefa de banco de dados: {{.Job.Name}}</h1>
<form method="post" action="{{.Base}}/jobs/{{.JobID}}/update">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <input type="hidden" name="kind" value="database">
  <div>Nome: <input type="text" name="name" value="{{.Job.Name}}" required></div>
  <div>Engine:
    <select name="engine">
      <option value="postgresql" {{if eq .Job.Engine "postgresql"}}selected{{end}}>PostgreSQL</option>
      <option value="mysql" {{if eq .Job.Engine "mysql"}}selected{{end}}>MySQL</option>
      <option value="mariadb" {{if eq .Job.Engine "mariadb"}}selected{{end}}>MariaDB</option>
      <option value="sqlite" {{if eq .Job.Engine "sqlite"}}selected{{end}}>SQLite</option>
    </select>
  </div>
  <div>Host: <input type="text" name="host" value="{{.Job.Host}}"></div>
  <div>Porta: <input type="number" name="port" value="{{.Job.Port}}"></div>
  <div>Usuário: <input type="text" name="username" value="{{.Job.Username}}"></div>
  <div>Senha (não é salva; preencha de novo se este job foi recriado com credencial nova): <input type="password" name="password"></div>
  <div>Banco específico: <input type="text" name="database" value="{{.Job.Database}}"></div>
  <div><label><input type="checkbox" name="all_databases"> Todos os bancos (ignora o campo acima)</label></div>
  <div>Caminho SQLite (só para engine=sqlite):
    <div class="gb-path-row">
      <input type="text" id="edb_sqlitepath" name="sqlite_path" value="{{.Job.SQLitePath}}">
      <button type="button" class="secondary" onclick="gbOpenPicker('edb_sqlitepath','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
  <div>Compressão:
    <select name="compression">
      <option value="gzip" {{if eq .Job.Compression "gzip"}}selected{{end}}>gzip</option>
      <option value="zip" {{if eq .Job.Compression "zip"}}selected{{end}}>zip</option>
      <option value="none" {{if eq .Job.Compression "none"}}selected{{end}}>nenhuma</option>
    </select>
  </div>
  <div>Destino (diretório):
    <div class="gb-path-row">
      <input type="text" id="edb_dest" name="dest_path" value="{{.Job.DestPath}}" required>
      <button type="button" class="secondary" onclick="gbOpenPicker('edb_dest','{{.Base}}')">Escolher diretório…</button>
    </div>
  </div>
  <div>Tipo de destino:
    <select name="dest_type">
      <option value="local" {{if eq .Job.DestType "local"}}selected{{end}}>Local</option>
      <option value="nas" {{if eq .Job.DestType "nas"}}selected{{end}}>NAS (montado)</option>
      <option value="ssh" {{if eq .Job.DestType "ssh"}}selected{{end}}>SSH remoto</option>
    </select>
  </div>
  <div>SSH host: <input type="text" name="ssh_host" value="{{.Job.SSHHost}}"></div>
  <div>SSH porta: <input type="number" name="ssh_port" value="{{.Job.SSHPort}}"></div>
  <div>SSH usuário: <input type="text" name="ssh_user" value="{{.Job.SSHUser}}"></div>
  <div>SSH chave privada (caminho no agente):
    <div class="gb-path-row">
      <input type="text" id="edb_sshkey" name="ssh_key" value="{{.Job.SSHKey}}">
      <button type="button" class="secondary" onclick="gbOpenPicker('edb_sshkey','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
  <div>Agendamento (cron, opcional — só funciona se o banco não exigir senha):
    <input type="text" name="cron" value="{{.Job.Cron}}" placeholder="minuto hora dia mês dia-da-semana">
  </div>
  <div><label><input type="checkbox" name="enabled" {{if .Job.Enabled}}checked{{end}}> Habilitada (desmarcar pausa o agendamento, sem apagar a tarefa)</label></div>
  <button type="submit">Salvar</button>
</form>
<p><a href="{{.Base}}/">&larr; voltar</a></p>
`))

var progressTmpl = template.Must(template.New("progress").Parse(`
<h1>Executando tarefa</h1>
<div id="gb-progress-wrap" style="max-width:560px;">
  <div style="background:#1b1b22; border:1px solid #333; border-radius:6px; height:28px; overflow:hidden; position:relative;">
    <div id="gb-progress-bar" style="height:100%; width:0%; background:#2d6cdf; transition: width 0.3s;"></div>
    <div id="gb-progress-label" style="position:absolute; inset:0; display:flex; align-items:center; justify-content:center; font-size:0.85rem;">iniciando…</div>
  </div>
  <p id="gb-progress-message" style="margin-top:0.8rem;"></p>
</div>
<p><a href="{{.Base}}/">&larr; voltar</a> (a tarefa continua rodando mesmo se você sair desta página)</p>
<style>
.gb-indeterminate { background: repeating-linear-gradient(45deg, #2d6cdf, #2d6cdf 10px, #3a7ef0 10px, #3a7ef0 20px); background-size: 28px 28px; animation: gb-stripes 1s linear infinite; width:100% !important; }
@keyframes gb-stripes { from { background-position: 0 0; } to { background-position: 28px 0; } }
</style>
<script>
var gbJobID = {{.JobID}};
var gbBase = {{.Base}};
function gbPollStatus() {
  fetch(gbBase + '/jobs/' + gbJobID + '/status', {credentials: 'same-origin'})
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var bar = document.getElementById('gb-progress-bar');
      var label = document.getElementById('gb-progress-label');
      var msg = document.getElementById('gb-progress-message');
      if (data.percent >= 0) {
        bar.classList.remove('gb-indeterminate');
        bar.style.width = data.percent + '%';
        label.textContent = data.percent + '%';
      } else {
        bar.classList.add('gb-indeterminate');
        label.textContent = data.status === 'running' ? 'em andamento…' : data.status;
      }
      msg.textContent = data.message || '';
      msg.className = data.status === 'failed' ? 'error' : (data.status === 'success' ? 'status-success' : '');
      if (data.status === 'success' || data.status === 'failed') {
        bar.style.background = data.status === 'success' ? '#4caf50' : '#f44336';
        bar.classList.remove('gb-indeterminate');
        if (data.percent < 0) { bar.style.width = '100%'; }
        return; // stop polling
      }
      setTimeout(gbPollStatus, 1000);
    })
    .catch(function() { setTimeout(gbPollStatus, 2000); });
}
gbPollStatus();
</script>
`))
