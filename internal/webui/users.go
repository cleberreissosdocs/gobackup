// Users administration screen: list/create/edit/delete accounts. Shared
// between gobackup-agent's own single-site UI and gobackup-central (wired
// in once, via Site.Mount, exactly like /login) — both store users in the
// same `users` table through the same *db.Store, so one implementation
// covers "menu de usuários do sistema" on both products.
package webui

import (
	"fmt"
	"html/template"
	"net/http"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/webauth"
)

func (s *Site) handleUsersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := s.Auth.Store.ListUsers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	currentID, _ := s.Auth.CurrentUserID(r)
	s.renderPage(w, r, "Usuários", usersListTmpl, map[string]any{
		"Users":     users,
		"CurrentID": currentID,
		"CSRF":      s.Auth.CSRFToken(r),
	})
}

func (s *Site) handleNewUserForm(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, "Novo usuário", userFormTmpl, map[string]any{
		"IsNew":  true,
		"CSRF":   s.Auth.CSRFToken(r),
		"Action": "/users/new",
	})
}

func (s *Site) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	r.ParseForm()
	email := r.FormValue("email")
	password := r.FormValue("password")
	role := core.UserRole(r.FormValue("role"))
	if email == "" || password == "" {
		s.renderPage(w, r, "Novo usuário", userFormTmpl, map[string]any{
			"IsNew": true, "CSRF": s.Auth.CSRFToken(r), "Action": "/users/new",
			"Error": "E-mail e senha são obrigatórios.",
		})
		return
	}
	if role != core.RoleAdmin && role != core.RoleOperator && role != core.RoleViewer {
		role = core.RoleViewer
	}
	hash, err := webauth.HashPassword(password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := s.Auth.Store.CreateUser(ctx, uuid.Nil, email, hash, role); err != nil {
		s.renderPage(w, r, "Novo usuário", userFormTmpl, map[string]any{
			"IsNew": true, "CSRF": s.Auth.CSRFToken(r), "Action": "/users/new",
			"Error": err.Error(), "Email": email,
		})
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (s *Site) handleEditUserForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := uuid.Parse(r.PathValue("userID"))
	if err != nil {
		http.Error(w, "id de usuário inválido", http.StatusBadRequest)
		return
	}
	u, err := s.Auth.Store.GetUserByID(ctx, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.renderPage(w, r, "Editar usuário", userFormTmpl, map[string]any{
		"IsNew": false, "CSRF": s.Auth.CSRFToken(r),
		"Action": fmt.Sprintf("/users/%s/edit", u.ID), "EditUser": u,
	})
}

func (s *Site) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := uuid.Parse(r.PathValue("userID"))
	if err != nil {
		http.Error(w, "id de usuário inválido", http.StatusBadRequest)
		return
	}
	r.ParseForm()
	role := core.UserRole(r.FormValue("role"))
	password := r.FormValue("password")

	// Guard: never let the last admin demote themselves (or be demoted),
	// which would lock every user out of user management with no way back
	// in short of a direct database edit.
	if role != core.RoleAdmin {
		u, err := s.Auth.Store.GetUserByID(ctx, userID)
		if err == nil && u.Role == core.RoleAdmin {
			if n, err := s.Auth.Store.CountAdmins(ctx); err == nil && n <= 1 {
				s.renderPage(w, r, "Editar usuário", userFormTmpl, map[string]any{
					"IsNew": false, "CSRF": s.Auth.CSRFToken(r),
					"Action": fmt.Sprintf("/users/%s/edit", userID), "EditUser": u,
					"Error": "Não é possível rebaixar o último administrador — crie outro admin antes.",
				})
				return
			}
		}
	}

	if role == core.RoleAdmin || role == core.RoleOperator || role == core.RoleViewer {
		if err := s.Auth.Store.UpdateUserRole(ctx, userID, role); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if password != "" {
		hash, err := webauth.HashPassword(password)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.Auth.Store.UpdateUserPassword(ctx, userID, hash); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (s *Site) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := uuid.Parse(r.PathValue("userID"))
	if err != nil {
		http.Error(w, "id de usuário inválido", http.StatusBadRequest)
		return
	}
	if currentID, ok := s.Auth.CurrentUserID(r); ok && currentID == userID {
		http.Error(w, "você não pode apagar seu próprio usuário enquanto está logado com ele", http.StatusBadRequest)
		return
	}
	u, err := s.Auth.Store.GetUserByID(ctx, userID)
	if err == nil && u.Role == core.RoleAdmin {
		if n, err := s.Auth.Store.CountAdmins(ctx); err == nil && n <= 1 {
			http.Error(w, "não é possível apagar o último administrador", http.StatusBadRequest)
			return
		}
	}
	if err := s.Auth.Store.DeleteUser(ctx, userID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

var roleLabels = map[core.UserRole]string{
	core.RoleAdmin:    "Administrador",
	core.RoleOperator: "Operador",
	core.RoleViewer:   "Visualizador",
}

var usersFuncs = template.FuncMap{
	"roleLabel": func(role core.UserRole) string {
		if l, ok := roleLabels[role]; ok {
			return l
		}
		return string(role)
	},
}

var usersListTmpl = template.Must(template.New("users-list").Funcs(usersFuncs).Parse(`
<h1>Usuários</h1>
<p><a href="/users/new"><button type="button">+ Novo usuário</button></a></p>
<div class="gb-card">
<table>
<tr><th>E-mail</th><th>Perfil</th><th>Criado em</th><th></th></tr>
{{$currentID := .CurrentID}}
{{$csrf := .CSRF}}
{{range .Users}}
<tr>
  <td>{{.Email}}{{if eq .ID $currentID}} <small>(você)</small>{{end}}</td>
  <td>{{roleLabel .Role}}</td>
  <td><small>{{.CreatedAt.Format "2006-01-02 15:04"}}</small></td>
  <td style="white-space:nowrap;">
    <a href="/users/{{.ID}}/edit">Editar</a>
    {{if ne .ID $currentID}}
    <span style="display:inline-block; width:1rem;"></span>
    <form method="post" action="/users/{{.ID}}/delete" style="display:inline" onsubmit="return confirm('Apagar o usuário {{.Email}}? Essa ação não pode ser desfeita.');">
      <input type="hidden" name="csrf_token" value="{{$csrf}}">
      <button type="submit" class="secondary">Apagar</button>
    </form>
    {{end}}
  </td>
</tr>
{{else}}
<tr><td colspan="4">nenhum usuário cadastrado</td></tr>
{{end}}
</table>
</div>
`))

var userFormTmpl = template.Must(template.New("user-form").Funcs(usersFuncs).Parse(`
<h1>{{if .IsNew}}Novo usuário{{else}}Editar usuário{{end}}</h1>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<form method="post" action="{{.Action}}">
  <input type="hidden" name="csrf_token" value="{{.CSRF}}">
  {{if .IsNew}}
  <div>E-mail: <input type="text" name="email" value="{{.Email}}" required></div>
  <div>Senha: <input type="password" name="password" required></div>
  {{else}}
  <div>E-mail: <input type="text" value="{{.EditUser.Email}}" disabled></div>
  <div>Nova senha <small>(deixe em branco para manter a atual)</small>: <input type="password" name="password"></div>
  {{end}}
  <div>Perfil:
    <select name="role">
      <option value="admin" {{if and (not .IsNew) (eq .EditUser.Role "admin")}}selected{{end}}>Administrador — acesso total, inclusive gerenciar usuários</option>
      <option value="operator" {{if and (not .IsNew) (eq .EditUser.Role "operator")}}selected{{end}}>Operador — cria/edita/executa tarefas, sem gerenciar usuários</option>
      <option value="viewer" {{if and (not .IsNew) (eq .EditUser.Role "viewer")}}selected{{end}}>Visualizador — apenas acompanha status e histórico</option>
    </select>
  </div>
  <button type="submit">Salvar</button>
</form>
<p><a href="/users">&larr; voltar</a></p>
`))
