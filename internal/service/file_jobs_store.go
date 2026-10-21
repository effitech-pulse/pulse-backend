package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// FileJobStore — транзакционная граница приватных файловых заданий.
type FileJobStore interface {
	AcceptFileJob(context.Context, domain.FileJobPrepared) (domain.FileJobStatus, error)
	FileJob(context.Context, int64, string) (domain.FileJobStatus, error)
	FileJobs(context.Context, int64, string) (domain.FileJobPage, error)
	FileJobResult(context.Context, int64, string) (domain.FileSavedCheck, error)
	CancelFileJob(context.Context, int64, string, int64) (domain.FileJobStatus, error)
	ClaimFileJobPublish(context.Context) (*domain.FileJobPublishLease, error)
	FinishFileJobPublish(context.Context, domain.FileJobPublishLease, domain.FileJobPublishOutcome) error
	ClaimFileJob(context.Context, domain.FileJobMessage, string) (*domain.FileJobLease, error)
	ExecutionFileJob(context.Context, domain.FileJobLease) (domain.FileJobExecution, error)
	RenewFileJob(context.Context, domain.FileJobLease) error
	FinishFileJob(context.Context, domain.FileJobLease, *domain.StoredFileResult, *domain.FileJobFailure) (domain.FileJobDisposition, error)
	ReconcileFileJobs(context.Context, int) (int, error)
	RedriveFileJob(context.Context, int64, string, int64, string) (domain.FileJobStatus, error)
}
