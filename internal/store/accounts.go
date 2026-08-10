package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type MegaAccountInput struct {
	ID                   string
	Label                string
	Email                string
	CredentialCiphertext []byte
	SessionCiphertext    []byte
	Status               string
	DefaultForDownloads  bool
}

type MegaAccountRecord struct {
	ID                  string     `json:"id"`
	Label               string     `json:"label"`
	Email               string     `json:"email"`
	Status              string     `json:"status"`
	DefaultForDownloads bool       `json:"defaultForDownloads"`
	LastCheckedAt       *time.Time `json:"lastCheckedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

func (d *DB) InsertMegaAccount(ctx context.Context, input MegaAccountInput, now time.Time) (MegaAccountRecord, error) {
	if input.ID == "" {
		var err error
		input.ID, err = newID()
		if err != nil {
			return MegaAccountRecord{}, err
		}
	}
	if input.Label == "" || input.Email == "" || len(input.CredentialCiphertext) == 0 {
		return MegaAccountRecord{}, fmt.Errorf("account label, email, and credential are required")
	}
	if input.Status == "" {
		input.Status = "unknown"
	}
	when := now.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	if input.DefaultForDownloads {
		if err := d.WithTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE mega_accounts SET default_for_downloads = 0`); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO mega_accounts(id,label,email,credential_ciphertext,session_ciphertext,status,default_for_downloads,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, input.ID, input.Label, input.Email, input.CredentialCiphertext, input.SessionCiphertext, input.Status, 1, when.Format(time.RFC3339Nano), when.Format(time.RFC3339Nano))
			return err
		}); err != nil {
			return MegaAccountRecord{}, fmt.Errorf("insert account: %w", err)
		}
	} else if _, err := d.ExecContext(ctx, `INSERT INTO mega_accounts(id,label,email,credential_ciphertext,session_ciphertext,status,default_for_downloads,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, input.ID, input.Label, input.Email, input.CredentialCiphertext, input.SessionCiphertext, input.Status, 0, when.Format(time.RFC3339Nano), when.Format(time.RFC3339Nano)); err != nil {
		return MegaAccountRecord{}, fmt.Errorf("insert account: %w", err)
	}
	return d.GetMegaAccount(ctx, input.ID)
}

func (d *DB) GetMegaAccount(ctx context.Context, id string) (MegaAccountRecord, error) {
	return d.scanMegaAccount(d.QueryRowContext(ctx, `SELECT id,label,email,status,default_for_downloads,last_checked_at,created_at,updated_at FROM mega_accounts WHERE id=?`, id))
}

func (d *DB) scanMegaAccount(row interface{ Scan(...any) error }) (MegaAccountRecord, error) {
	var r MegaAccountRecord
	var checked, created, updated sql.NullString
	var def int
	if err := row.Scan(&r.ID, &r.Label, &r.Email, &r.Status, &def, &checked, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MegaAccountRecord{}, ErrNotFound
		}
		return MegaAccountRecord{}, fmt.Errorf("read account: %w", err)
	}
	r.DefaultForDownloads = def != 0
	var err error
	r.CreatedAt, err = time.Parse(time.RFC3339Nano, created.String)
	if err != nil {
		return MegaAccountRecord{}, err
	}
	r.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated.String)
	if err != nil {
		return MegaAccountRecord{}, err
	}
	if checked.Valid {
		v, e := time.Parse(time.RFC3339Nano, checked.String)
		if e != nil {
			return MegaAccountRecord{}, e
		}
		r.LastCheckedAt = &v
	}
	return r, nil
}

func (d *DB) ListMegaAccounts(ctx context.Context) ([]MegaAccountRecord, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,label,email,status,default_for_downloads,last_checked_at,created_at,updated_at FROM mega_accounts ORDER BY label,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MegaAccountRecord
	for rows.Next() {
		r, err := d.scanMegaAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) MegaAccountSecrets(ctx context.Context, id string) (credential, session []byte, err error) {
	err = d.QueryRowContext(ctx, `SELECT COALESCE(credential_ciphertext,X''),COALESCE(session_ciphertext,X'') FROM mega_accounts WHERE id=?`, id).Scan(&credential, &session)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	return
}

func (d *DB) UpdateMegaAccount(ctx context.Context, id, label, email string, credential, session []byte, status string, def bool, now time.Time) error {
	when := now.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		if def {
			if _, err := tx.ExecContext(ctx, `UPDATE mega_accounts SET default_for_downloads=0`); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE mega_accounts SET label=?,email=?,credential_ciphertext=CASE WHEN length(?)>0 THEN ? ELSE credential_ciphertext END,session_ciphertext=CASE WHEN length(?)>0 THEN ? ELSE session_ciphertext END,status=?,default_for_downloads=?,updated_at=? WHERE id=?`, label, email, credential, credential, session, session, status, boolInt(def), when.Format(time.RFC3339Nano), id)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrNotFound
		}
		return nil
	})
}

func (d *DB) MarkMegaAccountChecked(ctx context.Context, id, status string, now time.Time) error {
	when := now.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	r, err := d.ExecContext(ctx, `UPDATE mega_accounts SET status=?,last_checked_at=?,updated_at=? WHERE id=?`, status, when.Format(time.RFC3339Nano), when.Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func (d *DB) DeleteMegaAccount(ctx context.Context, id string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		var references int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_jobs WHERE account_id=?`, id).Scan(&references); err != nil {
			return err
		}
		if references != 0 {
			return ErrRecordInUse
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM mega_accounts WHERE id=?`, id)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrNotFound
		}
		return nil
	})
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
