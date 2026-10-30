package webui

import "testing"

// Editing a job through the Central picks its form by trying the file
// getter first and falling back to the database one, so the getters MUST
// reject the wrong kind. Without that, a database job opened through the
// Central's remote administration rendered the file form: source path,
// exclusions and network-folder fields, none of which belong to it.
func TestKindMismatch(t *testing.T) {
	cases := []struct {
		nome string
		kind any
		want string
		quer bool
	}{
		{"tarefa de arquivos no formulário de arquivos", "files", "files", false},
		{"tarefa de banco no formulário de arquivos", "database", "files", true},
		{"tarefa de banco no formulário de banco", "database", "database", false},
		{"tarefa de arquivos no formulário de banco", "files", "database", true},

		// An agent too old to report the kind must stay editable rather
		// than become unreachable through the Central — "file first" was
		// already the fallback order.
		{"tipo ausente", nil, "files", false},
		{"tipo vazio", "", "database", false},
	}

	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			m := map[string]any{}
			if c.kind != nil {
				m["kind"] = c.kind
			}
			if got := kindMismatch(m, c.want); got != c.quer {
				t.Errorf("kindMismatch(kind=%v, quer=%q) = %v, esperado %v", c.kind, c.want, got, c.quer)
			}
		})
	}
}
