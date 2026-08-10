package download

import (
	"errors"
	"fmt"
)

type JobState string

const (
	JobQueued         JobState = "queued"
	JobResolving      JobState = "resolving"
	JobReady          JobState = "ready"
	JobDownloading    JobState = "downloading"
	JobPaused         JobState = "paused"
	JobPausedRecovery JobState = "paused_recovery"
	JobWaitingQuota   JobState = "waiting_quota"
	JobFinalizing     JobState = "finalizing"
	JobCompleted      JobState = "completed"
	JobFailed         JobState = "failed"
	JobCancelled      JobState = "cancelled"
)

type FileState string

const (
	FilePending     FileState = "pending"
	FileDownloading FileState = "downloading"
	FilePaused      FileState = "paused"
	FileWaiting     FileState = "waiting_quota"
	FileVerifying   FileState = "verifying"
	FileMoving      FileState = "moving"
	FileCompleted   FileState = "completed"
	FileFailed      FileState = "failed"
	FileCancelled   FileState = "cancelled"
)

var ErrInvalidStateTransition = errors.New("invalid download state transition")

// TransitionJobState is the only state transition rule used by the transfer
// manager. Recovery is deliberately represented explicitly so a restart never
// masquerades as a fresh queued job.
func TransitionJobState(from, to JobState) error {
	if from == to {
		return nil
	}
	allowed := map[JobState]map[JobState]bool{
		JobQueued:         {JobResolving: true, JobDownloading: true, JobPaused: true, JobCancelled: true},
		JobResolving:      {JobReady: true, JobQueued: true, JobPausedRecovery: true, JobFailed: true, JobCancelled: true},
		JobReady:          {JobQueued: true, JobDownloading: true, JobPaused: true, JobCancelled: true},
		JobDownloading:    {JobPaused: true, JobPausedRecovery: true, JobWaitingQuota: true, JobFinalizing: true, JobFailed: true, JobCancelled: true},
		JobPaused:         {JobQueued: true, JobDownloading: true, JobCancelled: true},
		JobPausedRecovery: {JobQueued: true, JobDownloading: true, JobCancelled: true},
		JobWaitingQuota:   {JobQueued: true, JobDownloading: true, JobPaused: true, JobCancelled: true, JobFailed: true},
		JobFinalizing:     {JobCompleted: true, JobFailed: true, JobPausedRecovery: true, JobCancelled: true},
		JobCompleted:      {},
		JobFailed:         {JobQueued: true, JobCancelled: true},
		JobCancelled:      {},
	}
	if allowed[from][to] {
		return nil
	}
	return fmt.Errorf("%w: job %q -> %q", ErrInvalidStateTransition, from, to)
}

// TransitionFileState applies the corresponding per-file lifecycle rules.
func TransitionFileState(from, to FileState) error {
	if from == to {
		return nil
	}
	allowed := map[FileState]map[FileState]bool{
		FilePending:     {FileDownloading: true, FilePaused: true, FileCancelled: true},
		FileDownloading: {FilePaused: true, FileWaiting: true, FileVerifying: true, FileFailed: true, FileCancelled: true},
		FilePaused:      {FileDownloading: true, FileCancelled: true},
		FileWaiting:     {FileDownloading: true, FilePaused: true, FileFailed: true, FileCancelled: true},
		FileVerifying:   {FileMoving: true, FileFailed: true, FilePaused: true},
		FileMoving:      {FileDownloading: true, FileCompleted: true, FileFailed: true, FilePaused: true},
		FileCompleted:   {},
		FileFailed:      {FileDownloading: true, FileCancelled: true},
		FileCancelled:   {},
	}
	if allowed[from][to] {
		return nil
	}
	return fmt.Errorf("%w: file %q -> %q", ErrInvalidStateTransition, from, to)
}

func IsTerminalJobState(state JobState) bool {
	return state == JobCompleted || state == JobFailed || state == JobCancelled
}

func IsActiveJobState(state JobState) bool {
	switch state {
	case JobResolving, JobDownloading, JobFinalizing:
		return true
	default:
		return false
	}
}

func IsTerminalFileState(state FileState) bool {
	return state == FileCompleted || state == FileFailed || state == FileCancelled
}
