package webui

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cleber/gobackup/internal/webauth"
)

// Site wires the shared handlers to either local (agent) or remote
// (central, per-agent) execution. GetRunner resolves which Runner (and
// display name) to use for the current request — for the agent it's always
// the same LocalRunner; for the central it depends on the {id} path value.
type Site struct {
	Auth      *webauth.Manager
	IsCentral bool
	GetRunner func(r *http.Request) (runner Runner, displayName string, err error)
	// BasePath is prefixed to every link rendered (e.g. "/ui/agents/<id>"
	// for central, "" for the agent's own single-site UI).
	BasePath func(r *http.Request) string
}

// Mount registers the shared admin screens under the given prefix pattern.
// prefix must end without a trailing slash, e.g. "" or "/agents/{id}".
func (s *Site) Mount(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.HandleFunc("GET "+prefix+"/", s.Auth.RequireLogin(s.handleHome))
	mux.HandleFunc("GET "+prefix+"/browse", s.Auth.RequireLogin(s.handleBrowse))
	mux.HandleFunc("GET "+prefix+"/browse.json", s.Auth.RequireLogin(s.handleBrowseJSON))
	mux.HandleFunc("POST "+prefix+"/jobs/file", s.Auth.RequireLoginAndCSRF(s.handleCreateFileJob))
	mux.HandleFunc("POST "+prefix+"/jobs/db", s.Auth.RequireLoginAndCSRF(s.handleCreateDBJob))
	mux.HandleFunc("GET "+prefix+"/jobs/{jobID}/edit", s.Auth.RequireLogin(s.handleEditJobForm))
	mux.HandleFunc("POST "+prefix+"/jobs/{jobID}/update", s.Auth.RequireLoginAndCSRF(s.handleUpdateJob))
	mux.HandleFunc("POST "+prefix+"/jobs/{jobID}/run", s.Auth.RequireLoginAndCSRF(s.handleRunJob))
	mux.HandleFunc("POST "+prefix+"/jobs/{jobID}/delete", s.Auth.RequireLoginAndCSRF(s.handleDeleteJob))
	mux.HandleFunc("GET "+prefix+"/jobs/{jobID}/status", s.Auth.RequireLogin(s.handleJobStatus))
}

func (s *Site) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, "Login", loginTmpl, nil)
}

func (s *Site) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	email := r.FormValue("email")
	password := r.FormValue("password")
	if err := s.Auth.Login(r.Context(), w, r, email, password); err != nil {
		s.renderPage(w, r, "Login", loginTmpl, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Site) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Site) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runner, name, err := s.GetRunner(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	activity, _ := runner.RecentActivity(ctx, 15)
	jobs, _ := runner.ListJobs(ctx)

	s.renderPage(w, r, "GoBackup — "+name, homeTmpl, map[string]any{
		"Name":     name,
		"Base":     s.base(r),
		"Activity": activity,
		"Jobs":     jobs,
		"CSRF":     s.Auth.CSRFToken(r),
	})
}

func (s *Site) handleBrowse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runner, name, err := s.GetRunner(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}

	entries, err := runner.ListDir(ctx, path)
	var errMsg string
	if err != nil {
		errMsg = err.Error()
	}

	s.renderPage(w, r, "Explorar diretório — "+name, browseTmpl, map[string]any{
		"Name":    name,
		"Base":    s.base(r),
		"Path":    path,
		"Entries": entries,
		"Error":   errMsg,
	})
}

// browseJSONResponse is what /browse.json returns for the JS directory
// picker (used by every origin/destination field in the forms).
type browseJSONResponse struct {
	Path    string     `json:"path"`
	Entries []DirEntry `json:"entries"`
	Error   string     `json:"error,omitempty"`
}

func (s *Site) handleBrowseJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runner, _, err := s.GetRunner(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(browseJSONResponse{Error: err.Error()})
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	path = filepath.Clean(path)

	entries, err := runner.ListDir(ctx, path)
	resp := browseJSONResponse{Path: path, Entries: entries}
	if err != nil {
		resp.Error = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func fileJobFromForm(r *http.Request) FileJobRequest {
	req := FileJobRequest{
		Name:       r.FormValue("name"),
		Source:     r.FormValue("source"),
		Dest:       r.FormValue("dest"),
		BackupType: r.FormValue("backup_type"),
		LinkDest:   r.FormValue("link_dest"),
		DestType:   r.FormValue("dest_type"),
		SSHHost:    r.FormValue("ssh_host"),
		SSHUser:    r.FormValue("ssh_user"),
		SSHKey:     r.FormValue("ssh_key"),
		Cron:       r.FormValue("cron"),
		Enabled:    r.FormValue("enabled") == "on",
	}
	req.SSHPort, _ = strconv.Atoi(r.FormValue("ssh_port"))
	return req
}

func dbJobFromForm(r *http.Request) DBJobRequest {
	req := DBJobRequest{
		Name:        r.FormValue("name"),
		Engine:      r.FormValue("engine"),
		Host:        r.FormValue("host"),
		Username:    r.FormValue("username"),
		Password:    r.FormValue("password"),
		Database:    r.FormValue("database"),
		SQLitePath:  r.FormValue("sqlite_path"),
		Compression: r.FormValue("compression"),
		DestPath:    r.FormValue("dest_path"),
		DestType:    r.FormValue("dest_type"),
		SSHHost:     r.FormValue("ssh_host"),
		SSHUser:     r.FormValue("ssh_user"),
		SSHKey:      r.FormValue("ssh_key"),
		Cron:        r.FormValue("cron"),
		Enabled:     r.FormValue("enabled") == "on",
	}
	req.Port, _ = strconv.Atoi(r.FormValue("port"))
	req.SSHPort, _ = strconv.Atoi(r.FormValue("ssh_port"))
	if r.FormValue("all_databases") == "on" {
		req.Database = "__ALL__"
	}
	return req
}

// handleCreateFileJob only PERSISTS the job definition — it does not run
// it. Use the "Executar" button on the saved job (handleRunJob) for that.
func (s *Site) handleCreateFileJob(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	runner, name, err := s.GetRunner(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.ParseForm()
	req := fileJobFromForm(r)
	req.Enabled = true // new jobs default to enabled (the checkbox only appears on Edit)

	var outcome JobOutcome
	if _, err := runner.CreateFileJob(ctx, req); err != nil {
		outcome = JobOutcome{Status: "failed", Message: err.Error()}
	} else {
		outcome = JobOutcome{Status: "success", Message: "Tarefa \"" + req.Name + "\" criada. Use o botão \"Executar\" na lista de tarefas para rodá-la."}
	}

	s.renderPage(w, r, "Resultado — "+name, outcomeTmpl, map[string]any{
		"Name": name, "Base": s.base(r), "Outcome": outcome, "JobName": req.Name,
	})
}

// handleCreateDBJob only PERSISTS the job definition — it does not run it
// (and the password is never stored; it's asked again when running).
func (s *Site) handleCreateDBJob(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	runner, name, err := s.GetRunner(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.ParseForm()
	req := dbJobFromForm(r)
	req.Enabled = true

	var outcome JobOutcome
	if _, err := runner.CreateDBJob(ctx, req); err != nil {
		outcome = JobOutcome{Status: "failed", Message: err.Error()}
	} else {
		outcome = JobOutcome{Status: "success", Message: "Tarefa \"" + req.Name + "\" criada. Use o botão \"Executar\" na lista de tarefas para rodá-la."}
	}

	s.renderPage(w, r, "Resultado — "+name, outcomeTmpl, map[string]any{
		"Name": name, "Base": s.base(r), "Outcome": outcome, "JobName": req.Name,
	})
}

// handleEditJobForm shows the prefilled edit form for an existing job
// (tries file first, falls back to database — GetFileJob/GetDBJob return an
// error for the wrong kind).
func (s *Site) handleEditJobForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runner, name, err := s.GetRunner(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	jobID := r.PathValue("jobID")

	if fileReq, ferr := runner.GetFileJob(ctx, jobID); ferr == nil {
		s.renderPage(w, r, "Editar tarefa — "+name, editFileTmpl, map[string]any{
			"Name": name, "Base": s.base(r), "JobID": jobID, "Job": fileReq, "CSRF": s.Auth.CSRFToken(r),
		})
		return
	}
	if dbReq, derr := runner.GetDBJob(ctx, jobID); derr == nil {
		s.renderPage(w, r, "Editar tarefa — "+name, editDBTmpl, map[string]any{
			"Name": name, "Base": s.base(r), "JobID": jobID, "Job": dbReq, "CSRF": s.Auth.CSRFToken(r),
		})
		return
	}
	http.Error(w, "tarefa não encontrada", http.StatusNotFound)
}

// handleUpdateJob saves an edited job — tries file first, then database.
func (s *Site) handleUpdateJob(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	runner, name, err := s.GetRunner(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	jobID := r.PathValue("jobID")
	r.ParseForm()
	kind := r.FormValue("kind")

	var outcome JobOutcome
	switch kind {
	case "files":
		req := fileJobFromForm(r)
		if err := runner.UpdateFileJob(ctx, jobID, req); err != nil {
			outcome = JobOutcome{Status: "failed", Message: err.Error()}
		} else {
			outcome = JobOutcome{Status: "success", Message: "Tarefa \"" + req.Name + "\" atualizada."}
		}
	case "database":
		req := dbJobFromForm(r)
		if err := runner.UpdateDBJob(ctx, jobID, req); err != nil {
			outcome = JobOutcome{Status: "failed", Message: err.Error()}
		} else {
			outcome = JobOutcome{Status: "success", Message: "Tarefa \"" + req.Name + "\" atualizada."}
		}
	default:
		outcome = JobOutcome{Status: "failed", Message: "tipo de tarefa desconhecido"}
	}

	s.renderPage(w, r, "Resultado — "+name, outcomeTmpl, map[string]any{
		"Name": name, "Base": s.base(r), "Outcome": outcome, "JobName": jobID,
	})
}

// handleRunJob STARTS an already-created job and renders a progress page
// that polls handleJobStatus every ~1s until it finishes — the "Executar"
// button next to each row in the "Tarefas" list on the home page.
func (s *Site) handleRunJob(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	runner, name, err := s.GetRunner(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	jobID := r.PathValue("jobID")
	r.ParseForm()
	password := r.FormValue("password")

	outcome, err := runner.RunJob(ctx, jobID, password)
	if err != nil {
		outcome = JobOutcome{Status: "failed", Message: err.Error()}
	}
	if outcome.Status == "failed" {
		s.renderPage(w, r, "Resultado — "+name, outcomeTmpl, map[string]any{
			"Name": name, "Base": s.base(r), "Outcome": outcome, "JobName": jobID,
		})
		return
	}

	s.renderPage(w, r, "Executando — "+name, progressTmpl, map[string]any{
		"Name": name, "Base": s.base(r), "JobID": jobID,
	})
}

// handleDeleteJob permanently removes a job definition (and its execution
// history) — the "Apagar" button next to each row in the "Tarefas" list.
// Does not touch files already written to past destinations.
func (s *Site) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	runner, _, err := s.GetRunner(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	jobID := r.PathValue("jobID")
	if err := runner.DeleteJob(ctx, jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, s.base(r)+"/", http.StatusSeeOther)
}

// progressStatusResponse is what /jobs/{id}/status returns for the
// progress bar's JS poller.
type progressStatusResponse struct {
	Status  string `json:"status"`
	Percent int    `json:"percent"`
	Message string `json:"message"`
}

func (s *Site) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runner, _, err := s.GetRunner(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(progressStatusResponse{Status: "failed", Percent: -1, Message: err.Error()})
		return
	}
	jobID := r.PathValue("jobID")
	info, err := runner.JobStatus(ctx, jobID)
	if err != nil {
		info = ProgressInfo{Status: "failed", Percent: -1, Message: err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(progressStatusResponse{Status: info.Status, Percent: info.Percent, Message: info.Message})
}

func (s *Site) base(r *http.Request) string {
	if s.BasePath != nil {
		return s.BasePath(r)
	}
	return ""
}

// renderPage wraps body in the shared layout, injecting the current user
// and whether to show the "Configurações" nav link (agent-only — the
// Central doesn't have a single set of connection settings to edit).
func (s *Site) renderPage(w http.ResponseWriter, r *http.Request, title string, body *template.Template, data map[string]any) {
	RenderPage(w, s.Auth, !s.IsCentral, r, title, body, data)
}

// RenderPage is the exported version of the shared-layout renderer, usable
// by agent-only screens (e.g. internal/agentweb's Settings page) that live
// outside the webui.Site abstraction but want the same chrome.
func RenderPage(w http.ResponseWriter, auth *webauth.Manager, showSettingsIfLoggedIn bool, r *http.Request, title string, body *template.Template, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if data == nil {
		data = map[string]any{}
	}
	user := auth.CurrentUser(r)
	data["Title"] = title
	data["User"] = user
	layoutTmpl.Execute(w, map[string]any{
		"Title":        title,
		"User":         user,
		"ShowSettings": showSettingsIfLoggedIn && user != "",
		"Body":         renderToString(body, data),
	})
}

func renderToString(t *template.Template, data map[string]any) template.HTML {
	var buf writerBuf
	_ = t.Execute(&buf, data)
	return template.HTML(buf.String())
}
