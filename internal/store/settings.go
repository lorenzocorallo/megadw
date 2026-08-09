package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type SettingValue struct {
	Key       string
	JSON      []byte
	UpdatedAt time.Time
}

func (d *DB) ReadSettings(ctx context.Context) (map[string]SettingValue, error) {
	rows, err := d.QueryContext(ctx, `SELECT key, value_json, updated_at FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	defer rows.Close()
	values := make(map[string]SettingValue)
	for rows.Next() {
		var key, raw, updated string
		if err := rows.Scan(&key, &raw, &updated); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		when, err := time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, fmt.Errorf("parse setting %q timestamp: %w", key, err)
		}
		values[key] = SettingValue{Key: key, JSON: []byte(raw), UpdatedAt: when}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read settings rows: %w", err)
	}
	return values, nil
}

func (d *DB) ReplaceSettings(ctx context.Context, values map[string][]byte, now time.Time) error {
	when := now.UTC().Format(time.RFC3339Nano)
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		for key, raw := range values {
			if key == "" || len(raw) == 0 {
				return fmt.Errorf("setting %q has no JSON value", key)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value_json, updated_at) VALUES (?, ?, ?)
                ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`, key, string(raw), when); err != nil {
				return fmt.Errorf("write setting %q: %w", key, err)
			}
		}
		return nil
	})
}
