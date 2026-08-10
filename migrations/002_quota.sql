-- Phase F durable quota scheduling and default proxy selection fields.
-- This migration remains separate so a Phase C database at version 1 can be
-- upgraded append-only by the embedded production runner.

ALTER TABLE download_jobs ADD COLUMN quota_next_retry_at TEXT;
ALTER TABLE download_jobs ADD COLUMN quota_retry_index INTEGER NOT NULL DEFAULT 0;
ALTER TABLE download_jobs ADD COLUMN last_error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE download_jobs ADD COLUMN last_error_message TEXT NOT NULL DEFAULT '';
ALTER TABLE proxy_profiles ADD COLUMN default_for_downloads INTEGER NOT NULL DEFAULT 0 CHECK(default_for_downloads IN (0, 1));
