package db

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
)

// dbPasswordKeyName is the app_secrets row holding the key that encrypts
// database passwords.
const dbPasswordKeyName = "db_password_key"

// HONEST LIMIT OF THIS ENCRYPTION
//
// The key lives in app_secrets, in the same database as the ciphertext.
// Anyone who can query this database can therefore decrypt every stored
// password, and this is not a defence against them. What it does buy:
// a SQL dump, a stolen disk, or a copy of the backup volume no longer
// hands over the passwords in plain text, and no password shows up in
// logs or in a casual `SELECT * FROM db_connections`.
//
// The alternative -- a key supplied per installation through the
// environment -- is genuinely stronger, but it makes every install carry
// a secret that must be set, transported and never lost, since losing it
// silently invalidates every stored password. That cost was judged not
// worth paying here. If this project ever stores passwords for systems
// outside the operator's own network, revisit this decision rather than
// assuming it still holds.

var (
	keyOnce   sync.Once
	cachedKey []byte
	keyErr    error
)

// secretKey returns the database-password key, creating it on first use.
//
// The insert tolerates a concurrent creator (ON CONFLICT DO NOTHING
// followed by a re-read) so two agents starting at once cannot end up
// with different keys, which would make one of them unable to read the
// other's passwords.
func (s *Store) secretKey(ctx context.Context) ([]byte, error) {
	keyOnce.Do(func() {
		var key []byte
		err := s.Pool.QueryRow(ctx, `SELECT value FROM app_secrets WHERE name=$1`, dbPasswordKeyName).Scan(&key)
		if err == nil && len(key) == 32 {
			cachedKey = key
			return
		}

		fresh := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, fresh); err != nil {
			keyErr = fmt.Errorf("gerar chave de senha: %w", err)
			return
		}
		if _, err := s.Pool.Exec(ctx,
			`INSERT INTO app_secrets (name, value) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`,
			dbPasswordKeyName, fresh); err != nil {
			keyErr = fmt.Errorf("gravar chave de senha: %w", err)
			return
		}
		// Re-read rather than trusting `fresh`: if another process won
		// the race, the stored key is theirs, not ours.
		if err := s.Pool.QueryRow(ctx, `SELECT value FROM app_secrets WHERE name=$1`, dbPasswordKeyName).Scan(&key); err != nil {
			keyErr = fmt.Errorf("ler chave de senha: %w", err)
			return
		}
		cachedKey = key
	})
	return cachedKey, keyErr
}

// encryptSecret seals plaintext with AES-256-GCM, returning nonce||ciphertext.
func encryptSecret(key []byte, plaintext string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("gerar nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// decryptSecret opens a value produced by encryptSecret.
func decryptSecret(key, sealed []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", fmt.Errorf("valor cifrado truncado (%d bytes)", len(sealed))
	}
	nonce, body := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		// Most likely cause: the key in app_secrets was replaced, so the
		// stored passwords are unreadable and must be retyped. Say that
		// instead of leaking a bare "message authentication failed".
		return "", fmt.Errorf("não foi possível decifrar a senha guardada (a chave mudou? a senha precisa ser digitada de novo): %w", err)
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cifra: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return gcm, nil
}

// EncryptDBPassword prepares a password for storage. An empty password
// yields a nil value, which the callers treat as "keep what is there".
func (s *Store) EncryptDBPassword(ctx context.Context, password string) ([]byte, error) {
	if password == "" {
		return nil, nil
	}
	key, err := s.secretKey(ctx)
	if err != nil {
		return nil, err
	}
	return encryptSecret(key, password)
}

// GetDBJobPassword returns the stored password for a database job, or an
// empty string when none was ever saved.
func (s *Store) GetDBJobPassword(ctx context.Context, jobID interface{ String() string }) (string, error) {
	var sealed []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT dc.password_enc
		FROM backup_jobs bj
		JOIN db_connections dc ON dc.id = bj.db_connection_id
		WHERE bj.id=$1 AND bj.job_kind='database'
	`, jobID.String()).Scan(&sealed)
	if err != nil {
		return "", fmt.Errorf("ler senha guardada: %w", err)
	}
	if len(sealed) == 0 {
		return "", nil
	}
	key, err := s.secretKey(ctx)
	if err != nil {
		return "", err
	}
	return decryptSecret(key, sealed)
}
