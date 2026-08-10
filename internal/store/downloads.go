package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type DownloadJobInput struct {
	ID                  string
	SourceKind          string
	SourceHandle        string
	SourceKeyCiphertext []byte
	SourceSelectedPath  string
	SourceSelectedNode  string
	DisplayName         string
	TotalBytes          int64
	AccountID           string
	ProxyID             string
	DestinationSubdir   string
	CompleteRoot        string
	IncompleteRoot      string
	State               string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Files               []DownloadFileInput
}

type DownloadFileInput struct {
	ID                   string
	RemoteNodeID         string
	RemotePath           string
	FinalRelativePath    string
	SizeBytes            int64
	SegmentSizeBytes     int64
	SegmentCount         int64
	FileKeyCiphertext    []byte
	PayloadURLCiphertext []byte
	PayloadContext       string
	State                string
}

type DownloadJobRecord struct {
	ID                  string               `json:"id"`
	SourceKind          string               `json:"sourceKind"`
	SourceHandle        string               `json:"sourceHandle"`
	SourceSelectedPath  string               `json:"-"`
	SourceSelectedNode  string               `json:"-"`
	DisplayName         string               `json:"displayName"`
	TotalBytes          int64                `json:"totalBytes"`
	AccountID           string               `json:"accountId,omitempty"`
	ProxyID             string               `json:"proxyId,omitempty"`
	DestinationSubdir   string               `json:"destinationSubdirectory"`
	CompleteRoot        string               `json:"completeRoot"`
	IncompleteRoot      string               `json:"incompleteRoot"`
	State               string               `json:"state"`
	CreatedAt           time.Time            `json:"createdAt"`
	UpdatedAt           time.Time            `json:"updatedAt"`
	SourceKeyCiphertext []byte               `json:"-"`
	Files               []DownloadFileRecord `json:"files"`
}

type DownloadFileRecord struct {
	ID                   string     `json:"id"`
	JobID                string     `json:"jobId"`
	RemoteNodeID         string     `json:"remoteNodeId"`
	RemotePath           string     `json:"remotePath"`
	FinalRelativePath    string     `json:"finalRelativePath"`
	SizeBytes            int64      `json:"sizeBytes"`
	SegmentSizeBytes     int64      `json:"segmentSizeBytes"`
	SegmentCount         int64      `json:"segmentCount"`
	CompletedBitmap      []byte     `json:"-"`
	BytesCommitted       int64      `json:"bytesCommitted"`
	State                string     `json:"state"`
	LastErrorCode        string     `json:"lastErrorCode,omitempty"`
	LastErrorMessage     string     `json:"lastErrorMessage,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	CompletedAt          *time.Time `json:"completedAt,omitempty"`
	FileKeyCiphertext    []byte     `json:"-"`
	PayloadURLCiphertext []byte     `json:"-"`
	PayloadContext       string     `json:"-"`
}

// DownloadFileCheckpoint is the durable portion of a file transfer. The
// downloader calls it only after syncing the corresponding partial file.
type DownloadFileCheckpoint struct {
	FileID           string
	CompletedBitmap  []byte
	BytesCommitted   int64
	State            string
	LastErrorCode    string
	LastErrorMessage string
	UpdatedAt        time.Time
}

// DownloadFileStateUpdate changes lifecycle state without touching the
// completion bitmap. It is used for recovery and final verification errors.
type DownloadFileStateUpdate struct {
	FileID           string
	State            string
	LastErrorCode    string
	LastErrorMessage string
	UpdatedAt        time.Time
}

func (d *DB) InsertDownloadJob(ctx context.Context, input DownloadJobInput) (DownloadJobRecord, error) {
	if input.ID == "" {
		var err error
		input.ID, err = newID()
		if err != nil {
			return DownloadJobRecord{}, err
		}
	}
	if len(input.Files) == 0 {
		return DownloadJobRecord{}, fmt.Errorf("download job must contain at least one file")
	}
	if input.SourceKind == "" || input.SourceHandle == "" || len(input.SourceKeyCiphertext) == 0 {
		return DownloadJobRecord{}, fmt.Errorf("download source is incomplete")
	}
	when := input.CreatedAt.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	updated := input.UpdatedAt.UTC()
	if updated.IsZero() {
		updated = when
	}
	if input.State == "" {
		input.State = "ready"
	}
	err := d.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO download_jobs(
            id, source_kind, source_handle, source_key_ciphertext, source_selected_path,
            source_selected_node, display_name, total_bytes, account_id, proxy_id,
            destination_subdirectory, complete_root, incomplete_root, state, created_at, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?)`,
			input.ID, input.SourceKind, input.SourceHandle, input.SourceKeyCiphertext,
			input.SourceSelectedPath, input.SourceSelectedNode, input.DisplayName, input.TotalBytes,
			input.AccountID, input.ProxyID, input.DestinationSubdir, input.CompleteRoot, input.IncompleteRoot,
			input.State, when.Format(time.RFC3339Nano), updated.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("insert download job: %w", err)
		}
		for index, file := range input.Files {
			if file.ID == "" {
				file.ID, err = newID()
				if err != nil {
					return err
				}
				input.Files[index].ID = file.ID
			}
			if file.State == "" {
				file.State = "pending"
			}
			if len(file.FileKeyCiphertext) == 0 {
				return fmt.Errorf("file %q has no encrypted key", file.RemoteNodeID)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO download_files(
                id, job_id, remote_node_id, remote_path, final_relative_path, size_bytes,
                segment_size_bytes, segment_count, completed_segments_bitmap, bytes_committed,
                file_key_ciphertext, payload_url_ciphertext, payload_context, state,
                last_error_code, last_error_message, created_at, updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, X'', 0, ?, NULLIF(?, X''), ?, ?, '', '', ?, ?)`,
				file.ID, input.ID, file.RemoteNodeID, file.RemotePath, file.FinalRelativePath, file.SizeBytes,
				file.SegmentSizeBytes, file.SegmentCount, file.FileKeyCiphertext, file.PayloadURLCiphertext,
				file.PayloadContext, file.State, when.Format(time.RFC3339Nano), updated.Format(time.RFC3339Nano)); err != nil {
				return fmt.Errorf("insert download file %q: %w", file.RemoteNodeID, err)
			}
		}
		return nil
	})
	if err != nil {
		return DownloadJobRecord{}, err
	}
	return d.GetDownloadJob(ctx, input.ID)
}

func (d *DB) GetDownloadJob(ctx context.Context, id string) (DownloadJobRecord, error) {
	var record DownloadJobRecord
	var created, updated string
	err := d.QueryRowContext(ctx, `SELECT id, source_kind, source_handle, source_key_ciphertext,
        source_selected_path, source_selected_node, display_name, total_bytes, COALESCE(account_id, ''),
        COALESCE(proxy_id, ''), destination_subdirectory, complete_root, incomplete_root, state,
        created_at, updated_at FROM download_jobs WHERE id = ?`, id).Scan(
		&record.ID, &record.SourceKind, &record.SourceHandle, &record.SourceKeyCiphertext,
		&record.SourceSelectedPath, &record.SourceSelectedNode, &record.DisplayName, &record.TotalBytes,
		&record.AccountID, &record.ProxyID, &record.DestinationSubdir, &record.CompleteRoot,
		&record.IncompleteRoot, &record.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return DownloadJobRecord{}, ErrNotFound
	}
	if err != nil {
		return DownloadJobRecord{}, fmt.Errorf("read download job: %w", err)
	}
	var parseErr error
	record.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, created)
	if parseErr != nil {
		return DownloadJobRecord{}, fmt.Errorf("parse download created_at: %w", parseErr)
	}
	record.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updated)
	if parseErr != nil {
		return DownloadJobRecord{}, fmt.Errorf("parse download updated_at: %w", parseErr)
	}
	record.SourceKeyCiphertext = append([]byte(nil), record.SourceKeyCiphertext...)
	rows, err := d.QueryContext(ctx, `SELECT id, job_id, remote_node_id, remote_path, final_relative_path,
        size_bytes, segment_size_bytes, segment_count, completed_segments_bitmap, bytes_committed,
        file_key_ciphertext, payload_url_ciphertext, payload_context, state, last_error_code,
        last_error_message, created_at, updated_at, completed_at FROM download_files WHERE job_id = ? ORDER BY id`, id)
	if err != nil {
		return DownloadJobRecord{}, fmt.Errorf("read download files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var file DownloadFileRecord
		var fileCreated, fileUpdated string
		var completed sql.NullString
		if err := rows.Scan(&file.ID, &file.JobID, &file.RemoteNodeID, &file.RemotePath, &file.FinalRelativePath,
			&file.SizeBytes, &file.SegmentSizeBytes, &file.SegmentCount, &file.CompletedBitmap, &file.BytesCommitted,
			&file.FileKeyCiphertext, &file.PayloadURLCiphertext, &file.PayloadContext, &file.State, &file.LastErrorCode,
			&file.LastErrorMessage, &fileCreated, &fileUpdated, &completed); err != nil {
			return DownloadJobRecord{}, fmt.Errorf("scan download file: %w", err)
		}
		file.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, fileCreated)
		if parseErr != nil {
			return DownloadJobRecord{}, fmt.Errorf("parse file created_at: %w", parseErr)
		}
		file.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, fileUpdated)
		if parseErr != nil {
			return DownloadJobRecord{}, fmt.Errorf("parse file updated_at: %w", parseErr)
		}
		if completed.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, completed.String)
			if parseErr != nil {
				return DownloadJobRecord{}, fmt.Errorf("parse file completed_at: %w", parseErr)
			}
			file.CompletedAt = &value
		}
		file.CompletedBitmap = append([]byte(nil), file.CompletedBitmap...)
		file.FileKeyCiphertext = append([]byte(nil), file.FileKeyCiphertext...)
		file.PayloadURLCiphertext = append([]byte(nil), file.PayloadURLCiphertext...)
		record.Files = append(record.Files, file)
	}
	if err := rows.Err(); err != nil {
		return DownloadJobRecord{}, fmt.Errorf("read download file rows: %w", err)
	}
	return record, nil
}

func (d *DB) ListDownloadJobs(ctx context.Context) ([]DownloadJobRecord, error) {
	rows, err := d.QueryContext(ctx, `SELECT id FROM download_jobs ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list download jobs: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan download job id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("list download job rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close download job rows: %w", err)
	}
	var result []DownloadJobRecord
	for _, id := range ids {
		record, err := d.GetDownloadJob(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

// CheckpointDownloadFiles persists a set of file snapshots in one SQLite
// transaction. Callers must have completed file.Sync before invoking this
// method; keeping that ordering outside the store makes the durability rule
// visible at the transfer boundary.
func (d *DB) CheckpointDownloadFiles(ctx context.Context, updates []DownloadFileCheckpoint) error {
	if len(updates) == 0 {
		return nil
	}
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		for _, update := range updates {
			if update.FileID == "" {
				return fmt.Errorf("checkpoint file id is required")
			}
			when := update.UpdatedAt.UTC()
			if when.IsZero() {
				when = time.Now().UTC()
			}
			state := update.State
			if state == "" {
				state = "downloading"
			}
			result, err := tx.ExecContext(ctx, `UPDATE download_files
                SET completed_segments_bitmap = ?, bytes_committed = ?, state = ?,
                    last_error_code = ?, last_error_message = ?, updated_at = ?
                WHERE id = ?`,
				[]byte(update.CompletedBitmap), update.BytesCommitted, state,
				update.LastErrorCode, update.LastErrorMessage, when.Format(time.RFC3339Nano), update.FileID)
			if err != nil {
				return fmt.Errorf("checkpoint download file %q: %w", update.FileID, err)
			}
			if affected, err := result.RowsAffected(); err != nil {
				return fmt.Errorf("check checkpoint file %q: %w", update.FileID, err)
			} else if affected != 1 {
				return fmt.Errorf("checkpoint file %q: %w", update.FileID, ErrNotFound)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE download_jobs SET updated_at = ?
                WHERE id = (SELECT job_id FROM download_files WHERE id = ?)`,
				when.Format(time.RFC3339Nano), update.FileID); err != nil {
				return fmt.Errorf("update checkpoint job for file %q: %w", update.FileID, err)
			}
		}
		return nil
	})
}

// UpdateDownloadFileState updates a lifecycle state and optional diagnostic.
func (d *DB) UpdateDownloadFileState(ctx context.Context, update DownloadFileStateUpdate) error {
	when := update.UpdatedAt.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	result, err := d.ExecContext(ctx, `UPDATE download_files
        SET state = ?, last_error_code = ?, last_error_message = ?, updated_at = ?
        WHERE id = ?`, update.State, update.LastErrorCode, update.LastErrorMessage,
		when.Format(time.RFC3339Nano), update.FileID)
	if err != nil {
		return fmt.Errorf("update download file %q: %w", update.FileID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check download file update %q: %w", update.FileID, err)
	} else if affected != 1 {
		return fmt.Errorf("download file %q: %w", update.FileID, ErrNotFound)
	}
	return nil
}

// PrepareDownloadFileMove durably records the resolved rename target before
// the atomic rename. If the process stops after rename but before completion
// persistence, restart recovery can verify that exact target instead of
// creating a new empty partial file or guessing a conflict suffix.
func (d *DB) PrepareDownloadFileMove(ctx context.Context, fileID, finalRelativePath string, when time.Time) error {
	when = when.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	result, err := d.ExecContext(ctx, `UPDATE download_files
		SET final_relative_path = ?, state = 'moving', updated_at = ?
		WHERE id = ?`, finalRelativePath, when.Format(time.RFC3339Nano), fileID)
	if err != nil {
		return fmt.Errorf("prepare download file move %q: %w", fileID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check prepared download file move %q: %w", fileID, err)
	} else if affected != 1 {
		return fmt.Errorf("download file %q: %w", fileID, ErrNotFound)
	}
	return nil
}

// CompleteDownloadFile records the actual final path only after the atomic
// rename has succeeded.
func (d *DB) CompleteDownloadFile(ctx context.Context, fileID, finalRelativePath string, when time.Time) error {
	when = when.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	result, err := d.ExecContext(ctx, `UPDATE download_files
        SET final_relative_path = ?, state = 'completed', completed_at = ?,
            bytes_committed = (SELECT size_bytes FROM download_files WHERE id = ?),
            last_error_code = '', last_error_message = '', updated_at = ?
        WHERE id = ?`, finalRelativePath, when.Format(time.RFC3339Nano),
		fileID, when.Format(time.RFC3339Nano), fileID)
	if err != nil {
		return fmt.Errorf("complete download file %q: %w", fileID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check completed download file %q: %w", fileID, err)
	} else if affected != 1 {
		return fmt.Errorf("download file %q: %w", fileID, ErrNotFound)
	}
	return nil
}

// SetDownloadJobState changes only the job lifecycle state. State transition
// validation lives in internal/download/state.go, where all callers share the
// same domain rules.
func (d *DB) SetDownloadJobState(ctx context.Context, jobID, state string, when time.Time) error {
	when = when.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	result, err := d.ExecContext(ctx, `UPDATE download_jobs SET state = ?, updated_at = ? WHERE id = ?`,
		state, when.Format(time.RFC3339Nano), jobID)
	if err != nil {
		return fmt.Errorf("update download job %q: %w", jobID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check download job update %q: %w", jobID, err)
	} else if affected != 1 {
		return fmt.Errorf("download job %q: %w", jobID, ErrNotFound)
	}
	return nil
}

// MarkDownloadsForRecovery makes active jobs safe to resume after a process
// restart. It intentionally leaves the persisted bitmap untouched.
func (d *DB) MarkDownloadsForRecovery(ctx context.Context, when time.Time) error {
	when = when.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE download_jobs
            SET state = 'paused_recovery', updated_at = ?
            WHERE state IN ('resolving', 'downloading', 'finalizing')`, when.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("mark download jobs for recovery: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE download_files
	            SET state = 'paused', updated_at = ?
	            WHERE state IN ('downloading', 'verifying')
	              AND job_id IN (SELECT id FROM download_jobs WHERE state = 'paused_recovery')`, when.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("mark download files for recovery: %w", err)
		}
		return nil
	})
}

// AddDownloadEvent stores a bounded diagnostic/state event. Progress events
// are deliberately not generated by the single-worker core.
func (d *DB) AddDownloadEvent(ctx context.Context, jobID, fileID, kind, message string, when time.Time) error {
	if jobID == "" || kind == "" {
		return fmt.Errorf("download event job and kind are required")
	}
	when = when.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO download_events(job_id, file_id, kind, message, created_at)
        VALUES (?, NULLIF(?, ''), ?, ?, ?)`, jobID, fileID, kind, message, when.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert download event: %w", err)
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM download_events
        WHERE job_id = ? AND id NOT IN (
            SELECT id FROM download_events WHERE job_id = ? ORDER BY id DESC LIMIT 500
        )`, jobID, jobID); err != nil {
		return fmt.Errorf("trim download events: %w", err)
	}
	return nil
}
