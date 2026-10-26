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
	// statusLabel translates a raw execution/command status into plain
	// Portuguese for display (the CSS class still uses the raw value, so
	// colors stay consistent with the rest of the app).
	"statusLabel": func(status string) string {
		switch status {
		case "success":
			return "sucesso"
		case "failed":
			return "falhou"
		case "running":
			return "em execução"
		case "cancelled":
			return "cancelado"
		case "pending":
			return "pendente"
		case "acked":
			return "confirmado"
		case "delivered":
			return "entregue"
		default:
			return status
		}
	},
	// scheduleBuilder renders the intuitive button-based scheduling widget
	// (replaces a raw cron text box). prefix must be unique per form on the
	// same page (e.g. "file", "db", "efile", "edb") so multiple instances
	// don't collide; cronValue pre-fills it when editing an existing job.
	"scheduleBuilder": func(prefix, cronValue string) template.HTML {
		return template.HTML(`<div class="gb-sched" data-prefix="` + template.HTMLEscapeString(prefix) + `" data-initial="` + template.HTMLEscapeString(cronValue) + `">
  <input type="hidden" name="cron" class="gb-sched-cron">
  <div class="gb-sched-freq">
    <button type="button" class="gb-sched-btn" data-freq="manual">Manual</button>
    <button type="button" class="gb-sched-btn" data-freq="daily">Diariamente</button>
    <button type="button" class="gb-sched-btn" data-freq="weekly">Semanalmente</button>
    <button type="button" class="gb-sched-btn" data-freq="monthly">Mensalmente</button>
    <button type="button" class="gb-sched-btn" data-freq="advanced">Avançado</button>
  </div>
  <div class="gb-sched-opt" data-opt="daily" hidden>
    <label>Horário <input type="time" class="gb-sched-time" value="03:00"></label>
  </div>
  <div class="gb-sched-opt" data-opt="weekly" hidden>
    <label>Horário <input type="time" class="gb-sched-time" value="03:00"></label>
    <div class="gb-sched-days">
      <button type="button" class="gb-sched-day" data-day="0">Dom</button>
      <button type="button" class="gb-sched-day" data-day="1">Seg</button>
      <button type="button" class="gb-sched-day" data-day="2">Ter</button>
      <button type="button" class="gb-sched-day" data-day="3">Qua</button>
      <button type="button" class="gb-sched-day" data-day="4">Qui</button>
      <button type="button" class="gb-sched-day" data-day="5">Sex</button>
      <button type="button" class="gb-sched-day" data-day="6">Sáb</button>
    </div>
  </div>
  <div class="gb-sched-opt" data-opt="monthly" hidden>
    <label>Dia do mês <input type="number" class="gb-sched-monthday" min="1" max="28" value="1"></label>
    <label>Horário <input type="time" class="gb-sched-time" value="03:00"></label>
  </div>
  <div class="gb-sched-opt" data-opt="advanced" hidden>
    <label>Expressão cron <input type="text" class="gb-sched-raw" placeholder="minuto hora dia mês dia-da-semana"></label>
  </div>
  <p class="gb-sched-summary"></p>
</div>`)
	},
}

var layoutTmpl = template.Must(template.New("layout").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<style>
* { box-sizing: border-box; }
:root {
  --gb-bg: #0b0c10;
  --gb-bg-elev: #15161c;
  --gb-card: #16171e;
  --gb-card-2: #1b1c24;
  --gb-border: #262835;
  --gb-border-soft: #1f2029;
  --gb-text: #e8e8ec;
  --gb-text-muted: #8a8f9c;
  --gb-text-faint: #5d6170;
  --gb-accent: #2d6cdf;
  --gb-accent-hover: #4a84ec;
  --gb-success: #2ecc71;
  --gb-success-bg: rgba(46,204,113,0.12);
  --gb-danger: #f0544a;
  --gb-danger-bg: rgba(240,84,74,0.12);
  --gb-warning: #e0a030;
  --gb-warning-bg: rgba(224,160,48,0.12);
  --gb-radius: 12px;
  --gb-radius-sm: 8px;
}
body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, Roboto, Helvetica, Arial, sans-serif;
  margin: 0;
  background: radial-gradient(circle at 15% 0%, #15161d 0%, var(--gb-bg) 55%);
  color: var(--gb-text);
  -webkit-font-smoothing: antialiased;
}
header {
  background: rgba(21,22,28,0.85);
  backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--gb-border-soft);
  padding: 0.9rem 2rem;
  display: flex; justify-content: space-between; align-items: center;
  position: sticky; top: 0; z-index: 10;
}
header > div:first-child a { display:flex; align-items:center; gap:0.55rem; font-weight:700; font-size:1.05rem; letter-spacing:-0.01em; }
header > div:first-child a::before { content:""; width:10px; height:10px; border-radius:50%; background: var(--gb-accent); box-shadow: 0 0 10px 1px rgba(45,108,223,0.7); display:inline-block; }
header a { color: var(--gb-text-muted); text-decoration: none; margin-right: 1.25rem; font-size: 0.92rem; transition: color .15s ease; }
header a:hover { color: var(--gb-text); }
header > div:last-child { display:flex; align-items:center; gap:0.9rem; font-size:0.88rem; color: var(--gb-text-muted); }
main { padding: 2.25rem 2rem 4rem; max-width: 1040px; margin: 0 auto; }
h1 { font-size: 1.6rem; font-weight: 700; letter-spacing: -0.015em; margin: 0 0 1.5rem; }
h3 { font-size: 1.05rem; font-weight: 600; margin: 0 0 1rem; }
input, select, button, textarea { font-size: 0.92rem; font-family: inherit; }
input[type=text], input[type=password], input[type=number], input[type=time], textarea, select {
  width: 100%; max-width: 420px; background: var(--gb-bg-elev); color: var(--gb-text);
  border: 1px solid var(--gb-border); border-radius: var(--gb-radius-sm); padding: 0.55rem 0.7rem;
  margin: 0.2rem 0; transition: border-color .15s ease, box-shadow .15s ease;
}
input:focus, select:focus, textarea:focus {
  outline: none; border-color: var(--gb-accent); box-shadow: 0 0 0 3px rgba(45,108,223,0.22);
}
input::placeholder, textarea::placeholder { color: var(--gb-text-faint); }
form > div, fieldset > div { display:flex; flex-direction:column; align-items:flex-start; gap:0.3rem; margin-bottom: 1.1rem; }
form > div:has(> label:only-child), fieldset > div:has(> label:only-child) { flex-direction: row; align-items:center; gap:0.5rem; }
.gb-path-row { display:flex; gap:0.5rem; align-items:center; max-width: 560px; width:100%; }
.gb-path-row input[type=text] { flex:1; margin:0; }
button {
  background: var(--gb-accent); color: #fff; border: none; padding: 0.55rem 1.1rem;
  cursor: pointer; border-radius: var(--gb-radius-sm); font-weight: 500;
  transition: background .15s ease, transform .1s ease;
}
button:hover { background: var(--gb-accent-hover); }
button:active { transform: translateY(1px); }
button.secondary { background: var(--gb-card-2); color: var(--gb-text); border: 1px solid var(--gb-border); }
button.secondary:hover { background: #24252f; }
button.danger { background: transparent; color: var(--gb-danger); border: 1px solid rgba(240,84,74,0.4); }
button.danger:hover { background: var(--gb-danger-bg); }
button:disabled { background: var(--gb-card-2) !important; color: var(--gb-text-faint) !important; border-color: var(--gb-border) !important; cursor: not-allowed; }
table { border-collapse: separate; border-spacing: 0; width: 100%; margin: 0.5rem 0 1rem; }
th, td { padding: 0.7rem 1rem; text-align: left; border-bottom: 1px solid var(--gb-border-soft); }
th { background: transparent; color: var(--gb-text-muted); font-size: 0.74rem; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600; border-bottom: 1px solid var(--gb-border); }
tbody tr { transition: background .1s ease; }
tbody tr:hover { background: rgba(255,255,255,0.02); }
tbody tr:last-child td { border-bottom: none; }
fieldset {
  border: 1px solid var(--gb-border);
  background: linear-gradient(160deg, var(--gb-card) 0%, var(--gb-bg-elev) 100%);
  margin-bottom: 1.75rem; border-radius: var(--gb-radius); padding: 1.6rem 1.75rem;
  box-shadow: 0 6px 20px rgba(0,0,0,0.25);
}
legend {
  padding: 0.15rem 0.7rem; color: var(--gb-accent); font-weight: 600; font-size: 0.78rem;
  text-transform: uppercase; letter-spacing: 0.05em; background: rgba(45,108,223,0.12); border-radius: 20px;
}
.status-success, .status-failed, .status-pending, .status-running, .status-acked, .status-delivered {
  display: inline-block; padding: 0.15rem 0.6rem; border-radius: 20px; font-size: 0.78rem; font-weight: 600; text-decoration: none;
}
a.status-running { cursor: pointer; }
a.status-running:hover { filter: brightness(1.15); }
.status-success { color: var(--gb-success); background: var(--gb-success-bg); }
.status-failed { color: var(--gb-danger); background: var(--gb-danger-bg); }
.status-pending, .status-running, .status-acked, .status-delivered { color: var(--gb-warning); background: var(--gb-warning-bg); }
.error { color: var(--gb-danger); }
.dir { color: #7fb4f5; }
.gb-sched { max-width: 560px; background: var(--gb-bg-elev); border:1px solid var(--gb-border); border-radius: var(--gb-radius-sm); padding:1rem; margin-top:0.3rem; }
.gb-sched-freq { display:flex; flex-wrap:wrap; gap:0.5rem; margin-bottom:0.2rem; }
.gb-sched-btn { background: var(--gb-card-2); color: var(--gb-text-muted); border:1px solid var(--gb-border); padding:0.4rem 0.9rem; border-radius:20px; font-size:0.85rem; }
.gb-sched-btn:hover { background:#262835; color: var(--gb-text); }
.gb-sched-btn.active { background: var(--gb-accent); border-color: var(--gb-accent); color:#fff; }
.gb-sched-opt { margin-top:0.9rem; display:flex; flex-wrap:wrap; align-items:center; gap:1rem; }
.gb-sched-opt[hidden] { display:none; }
.gb-sched-opt label { display:flex; flex-direction:column; gap:0.3rem; font-size:0.72rem; color: var(--gb-text-muted); text-transform:uppercase; letter-spacing:0.04em; }
.gb-sched-opt input { margin:0; width:auto; }
.gb-sched-days { display:flex; gap:0.4rem; }
.gb-sched-day { background: var(--gb-card-2); color: var(--gb-text-muted); border:1px solid var(--gb-border); padding:0.35rem 0.6rem; border-radius: var(--gb-radius-sm); font-size:0.8rem; }
.gb-sched-day.active { background: var(--gb-accent); border-color: var(--gb-accent); color:#fff; }
.gb-sched-summary { margin:0.8rem 0 0; font-size:0.84rem; color: var(--gb-text-muted); }
.gb-card {
  border: 1px solid var(--gb-border); background: linear-gradient(160deg, var(--gb-card) 0%, var(--gb-bg-elev) 100%);
  border-radius: var(--gb-radius); box-shadow: 0 6px 20px rgba(0,0,0,0.25);
}
.gb-kind-choice {
  flex: 1; min-width: 220px; background: var(--gb-bg-elev); border: 1px solid var(--gb-border);
  border-radius: var(--gb-radius-sm); padding: 1.5rem 1.25rem; cursor: pointer; text-align: center;
  display: flex; flex-direction: column; align-items: center; gap: 0.4rem;
  transition: border-color .15s ease, transform .1s ease, background .15s ease;
}
.gb-kind-choice:hover { border-color: var(--gb-accent); background: #1a1c25; transform: translateY(-2px); }
.gb-kind-icon { font-size: 2rem; }
.gb-kind-title { font-weight: 600; font-size: 1rem; color: var(--gb-text); }
.gb-kind-desc { font-size: 0.78rem; color: var(--gb-text-muted); }
small { color: var(--gb-text-muted); }
#gb-picker-modal { display:none; position:fixed; inset:0; background:rgba(5,6,9,0.75); backdrop-filter: blur(2px); align-items:center; justify-content:center; z-index:1000; }
#gb-picker-box { background: var(--gb-card); border:1px solid var(--gb-border); border-radius: var(--gb-radius); padding:1.5rem; width:90%; max-width:600px; max-height:80vh; display:flex; flex-direction:column; box-shadow: 0 20px 50px rgba(0,0,0,0.5); }
#gb-picker-list { overflow-y:auto; flex:1; margin:1rem 0; border:1px solid var(--gb-border); border-radius: var(--gb-radius-sm); padding:0.3rem; min-height: 200px; }
.gb-picker-item { padding:0.4rem 0.6rem; cursor:pointer; border-radius: 6px; font-size:0.9rem; }
.gb-picker-item:hover { background: var(--gb-card-2); }
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

// --- Schedule builder: turns each .gb-sched widget into button-based
// scheduling UI, writing the resulting cron expression into its hidden
// input. Multiple independent instances can coexist on one page (create
// file/db forms + edit forms), disambiguated by data-prefix.
var gbWeekdayNames = ['Dom','Seg','Ter','Qua','Qui','Sex','Sáb'];
function gbParseCron(expr) {
  var parts = (expr || '').trim().split(/\s+/);
  if (parts.length !== 5) return null;
  return { min: parts[0], hour: parts[1], dom: parts[2], mon: parts[3], dow: parts[4] };
}
function gbInitSchedule(root) {
  var cronInput = root.querySelector('.gb-sched-cron');
  var freqBtns = root.querySelectorAll('.gb-sched-btn');
  var opts = root.querySelectorAll('.gb-sched-opt');
  var summary = root.querySelector('.gb-sched-summary');
  var dayBtns = root.querySelectorAll('.gb-sched-day');
  var currentFreq = 'manual';

  function setFreq(freq) {
    currentFreq = freq;
    freqBtns.forEach(function(b) { b.classList.toggle('active', b.dataset.freq === freq); });
    opts.forEach(function(o) { o.hidden = (o.dataset.opt !== freq); });
    recompute();
  }

  function pad(n) { return (n < 10 ? '0' : '') + n; }

  function recompute() {
    var cron = '';
    if (currentFreq === 'manual') {
      cron = '';
      summary.textContent = 'Só roda quando clicar em "Executar" — sem agendamento automático.';
    } else if (currentFreq === 'daily') {
      var t = (root.querySelector('[data-opt="daily"] .gb-sched-time').value || '03:00').split(':');
      cron = parseInt(t[1],10) + ' ' + parseInt(t[0],10) + ' * * *';
      summary.textContent = 'Roda todo dia às ' + pad(parseInt(t[0],10)) + ':' + pad(parseInt(t[1],10)) + '.';
    } else if (currentFreq === 'weekly') {
      var t2 = (root.querySelector('[data-opt="weekly"] .gb-sched-time').value || '03:00').split(':');
      var days = [];
      dayBtns.forEach(function(b) { if (b.classList.contains('active')) days.push(b.dataset.day); });
      if (days.length === 0) {
        summary.textContent = 'Escolha pelo menos um dia da semana.';
        cronInput.value = '';
        return;
      }
      cron = parseInt(t2[1],10) + ' ' + parseInt(t2[0],10) + ' * * ' + days.join(',');
      var names = days.map(function(d) { return gbWeekdayNames[parseInt(d,10)]; });
      summary.textContent = 'Roda toda(o) ' + names.join(', ') + ' às ' + pad(parseInt(t2[0],10)) + ':' + pad(parseInt(t2[1],10)) + '.';
    } else if (currentFreq === 'monthly') {
      var dom = root.querySelector('[data-opt="monthly"] .gb-sched-monthday').value || '1';
      var t3 = (root.querySelector('[data-opt="monthly"] .gb-sched-time').value || '03:00').split(':');
      cron = parseInt(t3[1],10) + ' ' + parseInt(t3[0],10) + ' ' + dom + ' * *';
      summary.textContent = 'Roda todo dia ' + dom + ' do mês às ' + pad(parseInt(t3[0],10)) + ':' + pad(parseInt(t3[1],10)) + '.';
    } else if (currentFreq === 'advanced') {
      cron = root.querySelector('[data-opt="advanced"] .gb-sched-raw').value || '';
      summary.textContent = cron ? 'Expressão cron personalizada: ' + cron : 'Digite uma expressão cron de 5 campos.';
    }
    cronInput.value = cron;
  }

  freqBtns.forEach(function(b) {
    b.addEventListener('click', function() { setFreq(b.dataset.freq); });
  });
  dayBtns.forEach(function(b) {
    b.addEventListener('click', function() { b.classList.toggle('active'); recompute(); });
  });
  root.querySelectorAll('.gb-sched-time, .gb-sched-monthday, .gb-sched-raw').forEach(function(el) {
    el.addEventListener('input', recompute);
  });

  // Pre-fill from an existing cron value (edit forms).
  var initial = root.dataset.initial || '';
  var parsed = gbParseCron(initial);
  if (!initial) {
    setFreq('manual');
  } else if (parsed && parsed.dom === '*' && parsed.mon === '*' && parsed.dow === '*') {
    root.querySelector('[data-opt="daily"] .gb-sched-time').value = pad(parseInt(parsed.hour,10)) + ':' + pad(parseInt(parsed.min,10));
    setFreq('daily');
  } else if (parsed && parsed.dom === '*' && parsed.mon === '*' && parsed.dow !== '*') {
    root.querySelector('[data-opt="weekly"] .gb-sched-time').value = pad(parseInt(parsed.hour,10)) + ':' + pad(parseInt(parsed.min,10));
    parsed.dow.split(',').forEach(function(d) {
      var btn = root.querySelector('.gb-sched-day[data-day="' + d + '"]');
      if (btn) btn.classList.add('active');
    });
    setFreq('weekly');
  } else if (parsed && parsed.dom !== '*' && parsed.mon === '*' && parsed.dow === '*') {
    root.querySelector('[data-opt="monthly"] .gb-sched-monthday').value = parsed.dom;
    root.querySelector('[data-opt="monthly"] .gb-sched-time').value = pad(parseInt(parsed.hour,10)) + ':' + pad(parseInt(parsed.min,10));
    setFreq('monthly');
  } else {
    root.querySelector('[data-opt="advanced"] .gb-sched-raw').value = initial;
    setFreq('advanced');
  }
}
document.querySelectorAll('.gb-sched').forEach(gbInitSchedule);

// --- "Adicionar nova tarefa" flow: button -> choose kind -> show the
// matching form. Keeps the two full create-job forms out of view until
// the operator explicitly asks to create something, instead of showing
// both forms mixed together on the page at all times.
function gbShowNewJob() {
  var trigger = document.getElementById('gb-newjob-trigger');
  var choice = document.getElementById('gb-newjob-choice');
  if (!trigger || !choice) return;
  trigger.style.display = 'none';
  choice.style.display = 'block';
  document.getElementById('gb-newjob-file').style.display = 'none';
  document.getElementById('gb-newjob-db').style.display = 'none';
}
function gbHideNewJob() {
  var trigger = document.getElementById('gb-newjob-trigger');
  var choice = document.getElementById('gb-newjob-choice');
  if (!trigger || !choice) return;
  trigger.style.display = 'block';
  choice.style.display = 'none';
  document.getElementById('gb-newjob-file').style.display = 'none';
  document.getElementById('gb-newjob-db').style.display = 'none';
}
function gbShowJobForm(kind) {
  var choice = document.getElementById('gb-newjob-choice');
  if (!choice) return;
  choice.style.display = 'none';
  document.getElementById('gb-newjob-file').style.display = kind === 'file' ? 'block' : 'none';
  document.getElementById('gb-newjob-db').style.display = kind === 'db' ? 'block' : 'none';
}

// --- Database job form: only Host/Porta/Usuário/Senha/Banco específico/
// "Todos os bancos" make sense for a real DB server (Postgres/MySQL/
// MariaDB); SQLite is just a file, so it only needs "Caminho do arquivo
// SQLite". Toggle which fields are visible based on the chosen engine.
function gbToggleDBFields(select) {
  var form = select.closest('form');
  if (!form) return;
  var isSQLite = select.value === 'sqlite';
  form.querySelectorAll('.gb-db-server').forEach(function(el) { el.style.display = isSQLite ? 'none' : 'flex'; });
  form.querySelectorAll('.gb-db-sqlite').forEach(function(el) { el.style.display = isSQLite ? 'flex' : 'none'; });
}
document.querySelectorAll('.gb-db-engine').forEach(function(sel) { gbToggleDBFields(sel); });

// --- Destination type: SSH host/port/user/key only matter when "Tipo de
// destino" is SSH remoto — for Local or NAS (montado) it's just a path on
// this machine, so hide the SSH-only fields to avoid clutter/confusion.
function gbToggleSSHFields(select) {
  var form = select.closest('form');
  if (!form) return;
  var isSSH = select.value === 'ssh';
  form.querySelectorAll('.gb-dest-ssh').forEach(function(el) { el.style.display = isSSH ? 'flex' : 'none'; });
}
document.querySelectorAll('.gb-dest-type').forEach(function(sel) { gbToggleSSHFields(sel); });
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

<div id="gb-newjob-trigger">
  <button type="button" onclick="gbShowNewJob()">+ Adicionar nova tarefa</button>
</div>

<div id="gb-newjob-choice" class="gb-card" style="display:none; padding:1.75rem 1.5rem; margin-bottom:1.75rem;">
  <h3 style="margin-top:0;">Que tipo de tarefa você quer criar?</h3>
  <div style="display:flex; gap:1rem; flex-wrap:wrap; margin-bottom:1.2rem;">
    <button type="button" class="gb-kind-choice" onclick="gbShowJobForm('file')">
      <span class="gb-kind-icon">📁</span>
      <span class="gb-kind-title">Arquivos</span>
      <span class="gb-kind-desc">Copia diretórios via rsync (local, NAS ou SSH)</span>
    </button>
    <button type="button" class="gb-kind-choice" onclick="gbShowJobForm('db')">
      <span class="gb-kind-icon">🗄️</span>
      <span class="gb-kind-title">Banco de dados</span>
      <span class="gb-kind-desc">Dump de PostgreSQL, MySQL, MariaDB ou SQLite</span>
    </button>
  </div>
  <button type="button" class="secondary" onclick="gbHideNewJob()">Cancelar</button>
</div>

<fieldset id="gb-newjob-file" style="display:none;">
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
  <div><label><input type="hidden" name="delete_extraneous_present" value="1"><input type="checkbox" name="delete_extraneous" checked> Apagar no destino o que não existe mais na origem (mirror exato, rsync --delete)</label></div>
  <div>Tipo de destino:
    <select name="dest_type" class="gb-dest-type" onchange="gbToggleSSHFields(this)">
      <option value="local">Local</option>
      <option value="nas">NAS (montado)</option>
      <option value="ssh">SSH remoto</option>
    </select>
  </div>
  <div class="gb-dest-ssh">SSH host: <input type="text" name="ssh_host"></div>
  <div class="gb-dest-ssh">SSH porta: <input type="number" name="ssh_port" value="22"></div>
  <div class="gb-dest-ssh">SSH usuário: <input type="text" name="ssh_user"></div>
  <div class="gb-dest-ssh">SSH chave privada (caminho no agente):
    <div class="gb-path-row">
      <input type="text" id="file_sshkey" name="ssh_key">
      <button type="button" class="secondary" onclick="gbOpenPicker('file_sshkey','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
  <div>Agendamento:
    {{scheduleBuilder "file" ""}}
  </div>
  <button type="submit">Criar</button>
  <button type="button" class="secondary" onclick="gbShowNewJob()" style="margin-left:0.6rem;">&larr; voltar</button>
</form>
</fieldset>

<fieldset id="gb-newjob-db" style="display:none;">
<legend>Nova tarefa de banco de dados</legend>
<form method="post" action="{{.Base}}/jobs/db">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <div>Nome: <input type="text" name="name" required></div>
  <div>Engine:
    <select name="engine" class="gb-db-engine" onchange="gbToggleDBFields(this)">
      <option value="postgresql">PostgreSQL</option>
      <option value="mysql">MySQL</option>
      <option value="mariadb">MariaDB</option>
      <option value="sqlite">SQLite</option>
    </select>
  </div>
  <div class="gb-db-server">Host: <input type="text" name="host"></div>
  <div class="gb-db-server">Porta: <input type="number" name="port"></div>
  <div class="gb-db-server">Usuário: <input type="text" name="username"></div>
  <div class="gb-db-server">Senha: <input type="password" name="password"></div>
  <div class="gb-db-server">Banco específico: <input type="text" name="database"></div>
  <div class="gb-db-server"><label><input type="checkbox" name="all_databases"> Todos os bancos (ignora o campo acima)</label></div>
  <div class="gb-db-sqlite">Caminho do arquivo SQLite:
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
    <select name="dest_type" class="gb-dest-type" onchange="gbToggleSSHFields(this)">
      <option value="local">Local</option>
      <option value="nas">NAS (montado)</option>
      <option value="ssh">SSH remoto</option>
    </select>
  </div>
  <div class="gb-dest-ssh">SSH host: <input type="text" name="ssh_host"></div>
  <div class="gb-dest-ssh">SSH porta: <input type="number" name="ssh_port" value="22"></div>
  <div class="gb-dest-ssh">SSH usuário: <input type="text" name="ssh_user"></div>
  <div class="gb-dest-ssh">SSH chave privada (caminho no agente):
    <div class="gb-path-row">
      <input type="text" id="db_sshkey" name="ssh_key">
      <button type="button" class="secondary" onclick="gbOpenPicker('db_sshkey','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
  <div>Agendamento <small>(se o banco exigir senha, um backup agendado vai falhar porque a senha nunca fica salva — agende só sem autenticação, ou rode manualmente)</small>:
    {{scheduleBuilder "db" ""}}
  </div>
  <button type="submit">Criar</button>
  <button type="button" class="secondary" onclick="gbShowNewJob()" style="margin-left:0.6rem;">&larr; voltar</button>
</form>
</fieldset>

<fieldset>
<legend>Tarefas</legend>
<table>
<tr><th>Nome</th><th>Tipo</th><th>Agendamento</th><th>Status</th><th>Ação</th></tr>
{{range .Jobs}}
<tr>
  <td>{{.Name}}{{if not .Enabled}} <small>(desabilitada)</small>{{end}}</td>
  <td>{{if eq .Kind "files"}}Arquivos{{else}}Banco de dados{{end}}</td>
  <td>{{if .Cron}}{{.Cron}}{{else}}<small>manual</small>{{end}}</td>
  <td>{{if eq .Status "running"}}<a href="{{$.Base}}/jobs/{{.ID}}/progress" class="status-running">Executando</a>{{else if eq .Status "success"}}<span class="status-success">sucesso</span>{{else if eq .Status "failed"}}<span class="status-failed">falhou</span>{{else}}<small>nunca executada</small>{{end}}</td>
  <td style="white-space:nowrap;">
    <a href="{{$.Base}}/jobs/{{.ID}}/edit">Editar</a>
    <form method="post" action="{{$.Base}}/jobs/{{.ID}}/run" style="display:inline-flex; gap:0.5rem; align-items:center; margin-left:0.8rem">
      <input type="hidden" name="csrf_token" value="{{$.CSRF}}">
      {{if eq .Kind "database"}}<input type="password" name="password" placeholder="senha (se necessário)">{{end}}
      <button type="submit">Executar</button>
    </form>
    <span style="display:inline-block; width:1.5rem;"></span><span style="border-left:1px solid #444; padding-left:1rem;">
    <form method="post" action="{{$.Base}}/jobs/{{.ID}}/delete" style="display:inline" onsubmit="return confirm('Apagar a tarefa \'{{.Name}}\'? Isso remove a definição e o histórico de execuções (os arquivos já copiados em execuções anteriores NÃO são apagados).');">
      <input type="hidden" name="csrf_token" value="{{$.CSRF}}">
      <button type="submit" class="danger">Apagar</button>
    </form>
    </span>
  </td>
</tr>
{{else}}
<tr><td colspan="5">nenhuma tarefa criada ainda</td></tr>
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
  <td class="status-{{.Status}}">{{statusLabel .Status}}</td>
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

var editFileTmpl = template.Must(template.New("edit-file").Funcs(funcs).Parse(`
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
  <div><label><input type="hidden" name="delete_extraneous_present" value="1"><input type="checkbox" name="delete_extraneous" {{if .Job.DeleteExtraneous}}checked{{end}}> Apagar no destino o que não existe mais na origem (mirror exato, rsync --delete)</label></div>
  <div>Tipo de destino:
    <select name="dest_type" class="gb-dest-type" onchange="gbToggleSSHFields(this)">
      <option value="local" {{if eq .Job.DestType "local"}}selected{{end}}>Local</option>
      <option value="nas" {{if eq .Job.DestType "nas"}}selected{{end}}>NAS (montado)</option>
      <option value="ssh" {{if eq .Job.DestType "ssh"}}selected{{end}}>SSH remoto</option>
    </select>
  </div>
  <div class="gb-dest-ssh">SSH host: <input type="text" name="ssh_host" value="{{.Job.SSHHost}}"></div>
  <div class="gb-dest-ssh">SSH porta: <input type="number" name="ssh_port" value="{{.Job.SSHPort}}"></div>
  <div class="gb-dest-ssh">SSH usuário: <input type="text" name="ssh_user" value="{{.Job.SSHUser}}"></div>
  <div class="gb-dest-ssh">SSH chave privada (caminho no agente):
    <div class="gb-path-row">
      <input type="text" id="efile_sshkey" name="ssh_key" value="{{.Job.SSHKey}}">
      <button type="button" class="secondary" onclick="gbOpenPicker('efile_sshkey','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
  <div>Agendamento:
    {{scheduleBuilder "efile" .Job.Cron}}
  </div>
  <div><label><input type="checkbox" name="enabled" {{if .Job.Enabled}}checked{{end}}> Habilitada (desmarcar pausa o agendamento, sem apagar a tarefa)</label></div>
  <button type="submit">Salvar</button>
</form>
<p><a href="{{.Base}}/">&larr; voltar</a></p>
`))

var editDBTmpl = template.Must(template.New("edit-db").Funcs(funcs).Parse(`
<h1>Editar tarefa de banco de dados: {{.Job.Name}}</h1>
<form method="post" action="{{.Base}}/jobs/{{.JobID}}/update">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  <input type="hidden" name="kind" value="database">
  <div>Nome: <input type="text" name="name" value="{{.Job.Name}}" required></div>
  <div>Engine:
    <select name="engine" class="gb-db-engine" onchange="gbToggleDBFields(this)">
      <option value="postgresql" {{if eq .Job.Engine "postgresql"}}selected{{end}}>PostgreSQL</option>
      <option value="mysql" {{if eq .Job.Engine "mysql"}}selected{{end}}>MySQL</option>
      <option value="mariadb" {{if eq .Job.Engine "mariadb"}}selected{{end}}>MariaDB</option>
      <option value="sqlite" {{if eq .Job.Engine "sqlite"}}selected{{end}}>SQLite</option>
    </select>
  </div>
  <div class="gb-db-server">Host: <input type="text" name="host" value="{{.Job.Host}}"></div>
  <div class="gb-db-server">Porta: <input type="number" name="port" value="{{.Job.Port}}"></div>
  <div class="gb-db-server">Usuário: <input type="text" name="username" value="{{.Job.Username}}"></div>
  <div class="gb-db-server">Senha (não é salva; preencha de novo se este job foi recriado com credencial nova): <input type="password" name="password"></div>
  <div class="gb-db-server">Banco específico: <input type="text" name="database" value="{{.Job.Database}}"></div>
  <div class="gb-db-server"><label><input type="checkbox" name="all_databases"> Todos os bancos (ignora o campo acima)</label></div>
  <div class="gb-db-sqlite">Caminho do arquivo SQLite:
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
    <select name="dest_type" class="gb-dest-type" onchange="gbToggleSSHFields(this)">
      <option value="local" {{if eq .Job.DestType "local"}}selected{{end}}>Local</option>
      <option value="nas" {{if eq .Job.DestType "nas"}}selected{{end}}>NAS (montado)</option>
      <option value="ssh" {{if eq .Job.DestType "ssh"}}selected{{end}}>SSH remoto</option>
    </select>
  </div>
  <div class="gb-dest-ssh">SSH host: <input type="text" name="ssh_host" value="{{.Job.SSHHost}}"></div>
  <div class="gb-dest-ssh">SSH porta: <input type="number" name="ssh_port" value="{{.Job.SSHPort}}"></div>
  <div class="gb-dest-ssh">SSH usuário: <input type="text" name="ssh_user" value="{{.Job.SSHUser}}"></div>
  <div class="gb-dest-ssh">SSH chave privada (caminho no agente):
    <div class="gb-path-row">
      <input type="text" id="edb_sshkey" name="ssh_key" value="{{.Job.SSHKey}}">
      <button type="button" class="secondary" onclick="gbOpenPicker('edb_sshkey','{{.Base}}')">Escolher arquivo…</button>
    </div>
  </div>
  <div>Agendamento <small>(só funciona se o banco não exigir senha)</small>:
    {{scheduleBuilder "edb" .Job.Cron}}
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
