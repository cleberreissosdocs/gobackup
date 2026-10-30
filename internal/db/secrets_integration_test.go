package db

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These exercise the real migrations and the real queries against a
// throwaway PostgreSQL. They are skipped unless GOBACKUP_TEST_DB is set,
// so the normal `go test ./...` stays offline.
//
// What they protect: the password column is new, and the whole point of
// it is the UNATTENDED path -- a scheduled run has nobody to type a
// password, so if storage or retrieval is wrong the failure shows up at
// 3am, in a backup nobody is watching.
func storeDeTeste(t *testing.T) (*Store, uuid.UUID) {
	t.Helper()
	conn := os.Getenv("GOBACKUP_TEST_DB")
	if conn == "" {
		t.Skip("defina GOBACKUP_TEST_DB para rodar os testes com banco real")
	}
	if err := Migrate(conn); err != nil {
		t.Fatalf("migrar: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), conn)
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	t.Cleanup(pool.Close)

	s := &Store{Pool: pool}
	ctx := context.Background()

	var tenantID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ('teste') RETURNING id`).Scan(&tenantID); err != nil {
		t.Fatalf("criar tenant: %v", err)
	}
	var agentID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO agents (tenant_id, name, hostname, api_token_hash)
		 VALUES ($1, 'agente-teste', 'host-teste', 'hash-teste') RETURNING id`, tenantID).Scan(&agentID); err != nil {
		t.Fatalf("criar agente: %v", err)
	}
	return s, agentID
}

func TestSenhaGuardadaVoltaInteira(t *testing.T) {
	s, agentID := storeDeTeste(t)
	ctx := context.Background()

	const senha = "s3nh@-d0-b@nco*"
	connID, err := s.CreateDBConnection(ctx, agentID, "job1-connection", "postgresql", "127.0.0.1", 5432, "postgres", senha, "meubanco", "")
	if err != nil {
		t.Fatalf("criar conexão: %v", err)
	}
	var tenantID uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT tenant_id FROM agents WHERE id=$1`, agentID).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	jobID, err := s.CreateDatabaseJob(ctx, tenantID, agentID, connID, "job1", "gzip", "/tmp", "local", "", 0, "", "", "", 0)
	if err != nil {
		t.Fatalf("criar job: %v", err)
	}

	// Stored encrypted, not in the clear.
	var bruto []byte
	if err := s.Pool.QueryRow(ctx, `SELECT password_enc FROM db_connections WHERE id=$1`, connID).Scan(&bruto); err != nil {
		t.Fatal(err)
	}
	if len(bruto) == 0 {
		t.Fatal("nada foi gravado na coluna password_enc")
	}
	if string(bruto) == senha {
		t.Error("a senha foi gravada em texto puro")
	}

	got, err := s.GetDBJobPassword(ctx, jobID)
	if err != nil {
		t.Fatalf("ler senha: %v", err)
	}
	if got != senha {
		t.Errorf("senha voltou %q, esperado %q", got, senha)
	}
}

// The forms promise that a blank password keeps the saved one. If an
// edit wiped it, the next SCHEDULED run would fail — long after the edit.
func TestEdicaoEmBrancoMantemASenha(t *testing.T) {
	s, agentID := storeDeTeste(t)
	ctx := context.Background()

	const senha = "original-123"
	connID, err := s.CreateDBConnection(ctx, agentID, "job2-connection", "mysql", "127.0.0.1", 3306, "root", senha, "meubanco", "")
	if err != nil {
		t.Fatal(err)
	}
	var tenantID uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT tenant_id FROM agents WHERE id=$1`, agentID).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	jobID, err := s.CreateDatabaseJob(ctx, tenantID, agentID, connID, "job2", "gzip", "/tmp", "local", "", 0, "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Edit everything EXCEPT the password.
	if err := s.UpdateDatabaseJobByID(ctx, jobID, "job2", "mysql", "10.0.0.9", 3307, "outro", "",
		"outrobanco", "", "zip", "/var/backups", "local", "", 0, "", "", "0 3 * * *", 7, true); err != nil {
		t.Fatalf("atualizar: %v", err)
	}
	got, err := s.GetDBJobPassword(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if got != senha {
		t.Errorf("a edição apagou a senha: voltou %q, esperado %q", got, senha)
	}

	// A new password must actually replace it.
	if err := s.UpdateDatabaseJobByID(ctx, jobID, "job2", "mysql", "10.0.0.9", 3307, "outro", "nova-456",
		"outrobanco", "", "zip", "/var/backups", "local", "", 0, "", "", "0 3 * * *", 7, true); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetDBJobPassword(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "nova-456" {
		t.Errorf("a nova senha não foi gravada: voltou %q", got)
	}
}

// A job created before this feature has no stored password; reading it
// must return empty, not an error that would break the run entirely.
func TestJobSemSenhaGuardadaNaoDaErro(t *testing.T) {
	s, agentID := storeDeTeste(t)
	ctx := context.Background()

	connID, err := s.CreateDBConnection(ctx, agentID, "job3-connection", "sqlite", "", 0, "", "", "", "/tmp/x.db")
	if err != nil {
		t.Fatal(err)
	}
	var tenantID uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT tenant_id FROM agents WHERE id=$1`, agentID).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	jobID, err := s.CreateDatabaseJob(ctx, tenantID, agentID, connID, "job3", "gzip", "/tmp", "local", "", 0, "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDBJobPassword(ctx, jobID)
	if err != nil {
		t.Fatalf("job sem senha deveria devolver vazio, deu erro: %v", err)
	}
	if got != "" {
		t.Errorf("esperava vazio, veio %q", got)
	}
}
