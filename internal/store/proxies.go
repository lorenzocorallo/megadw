package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ProxyProfileInput struct {
	ID, Name, Type, Host         string
	Port                         int
	Username                     string
	PasswordCiphertext           []byte
	TimeoutSeconds               int
	Enabled, DefaultForDownloads bool
}
type ProxyProfileRecord struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Type                string    `json:"type"`
	Host                string    `json:"host"`
	Port                int       `json:"port"`
	Username            string    `json:"username,omitempty"`
	TimeoutSeconds      int       `json:"timeoutSeconds"`
	Enabled             bool      `json:"enabled"`
	DefaultForDownloads bool      `json:"defaultForDownloads"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

func (d *DB) InsertProxyProfile(ctx context.Context, in ProxyProfileInput, now time.Time) (ProxyProfileRecord, error) {
	if in.ID == "" {
		var e error
		in.ID, e = newID()
		if e != nil {
			return ProxyProfileRecord{}, e
		}
	}
	if in.Name == "" || in.Type == "" || in.Host == "" || in.Port < 1 || in.Port > 65535 {
		return ProxyProfileRecord{}, fmt.Errorf("proxy name, type, host, and valid port are required")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = 15
	}
	when := now.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	if err := d.WithTx(ctx, func(tx *sql.Tx) error {
		if in.DefaultForDownloads {
			if _, e := tx.ExecContext(ctx, `UPDATE proxy_profiles SET default_for_downloads=0`); e != nil {
				return e
			}
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO proxy_profiles(id,name,type,host,port,username,password_ciphertext,timeout_seconds,enabled,default_for_downloads,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.Name, in.Type, in.Host, in.Port, in.Username, in.PasswordCiphertext, in.TimeoutSeconds, boolInt(in.Enabled), boolInt(in.DefaultForDownloads), when.Format(time.RFC3339Nano), when.Format(time.RFC3339Nano))
		return e
	}); err != nil {
		return ProxyProfileRecord{}, fmt.Errorf("insert proxy: %w", err)
	}
	return d.GetProxyProfile(ctx, in.ID)
}

func (d *DB) GetProxyProfile(ctx context.Context, id string) (ProxyProfileRecord, error) {
	return d.scanProxy(d.QueryRowContext(ctx, `SELECT id,name,type,host,port,COALESCE(username,''),timeout_seconds,enabled,default_for_downloads,created_at,updated_at FROM proxy_profiles WHERE id=?`, id))
}
func (d *DB) scanProxy(row interface{ Scan(...any) error }) (ProxyProfileRecord, error) {
	var r ProxyProfileRecord
	var created, updated string
	var enabled, def int
	if err := row.Scan(&r.ID, &r.Name, &r.Type, &r.Host, &r.Port, &r.Username, &r.TimeoutSeconds, &enabled, &def, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProxyProfileRecord{}, ErrNotFound
		}
		return r, err
	}
	r.Enabled = enabled != 0
	r.DefaultForDownloads = def != 0
	var e error
	r.CreatedAt, e = time.Parse(time.RFC3339Nano, created)
	if e != nil {
		return r, e
	}
	r.UpdatedAt, e = time.Parse(time.RFC3339Nano, updated)
	return r, e
}
func (d *DB) ListProxyProfiles(ctx context.Context) ([]ProxyProfileRecord, error) {
	rows, e := d.QueryContext(ctx, `SELECT id,name,type,host,port,COALESCE(username,''),timeout_seconds,enabled,default_for_downloads,created_at,updated_at FROM proxy_profiles ORDER BY name,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []ProxyProfileRecord
	for rows.Next() {
		r, e := d.scanProxy(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (d *DB) ProxySecret(ctx context.Context, id string) ([]byte, error) {
	var b []byte
	e := d.QueryRowContext(ctx, `SELECT COALESCE(password_ciphertext,X'') FROM proxy_profiles WHERE id=?`, id).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, e
}
func (d *DB) UpdateProxyProfile(ctx context.Context, id string, in ProxyProfileInput, now time.Time) error {
	when := now.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		if in.DefaultForDownloads {
			if _, e := tx.ExecContext(ctx, `UPDATE proxy_profiles SET default_for_downloads=0`); e != nil {
				return e
			}
		}
		q := `UPDATE proxy_profiles SET name=?,type=?,host=?,port=?,username=?,password_ciphertext=CASE WHEN length(?)>0 THEN ? ELSE password_ciphertext END,timeout_seconds=?,enabled=?,default_for_downloads=?,updated_at=? WHERE id=?`
		r, e := tx.ExecContext(ctx, q, in.Name, in.Type, in.Host, in.Port, in.Username, in.PasswordCiphertext, in.PasswordCiphertext, in.TimeoutSeconds, boolInt(in.Enabled), boolInt(in.DefaultForDownloads), when.Format(time.RFC3339Nano), id)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrNotFound
		}
		return nil
	})
}
func (d *DB) DeleteProxyProfile(ctx context.Context, id string) error {
	r, e := d.ExecContext(ctx, `DELETE FROM proxy_profiles WHERE id=?`, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
