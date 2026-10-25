package service

import (
	"context"
	"io"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// PassportWorkbookCodec переводит XLSX в типизированные команды без доступа к БД.
type PassportWorkbookCodec interface {
	Decode(context.Context, io.Reader) (domain.PassportWorkbook, error)
	Encode(context.Context, io.Writer, domain.PassportExport) error
}

// EquipmentPassportStore повторно проверяет текущие права и scope в транзакции.
// Policy доверенно задаётся сервером; pure transition повторяется под commit locks.
type EquipmentPassportStore interface {
	ReadPassport(context.Context, domain.PassportPolicy, domain.PassportID) (domain.PassportView, error)
	PassportHistoryTable(context.Context, domain.PassportPolicy, domain.PassportID, domain.TableRequest) (domain.TableRows[domain.PassportEvent], error)
	PreparePassport(context.Context, domain.PassportPolicy, domain.PassportChangeSet, domain.PassportProvenance, domain.PassportDate) (domain.PassportPreview, error)
	ReadPassportPreview(context.Context, domain.PassportPolicy, domain.PassportID) (domain.PassportPreview, error)
	CancelPassportPreview(context.Context, domain.PassportPolicy, domain.PassportID) error
	CommitPassport(context.Context, domain.PassportPolicy, domain.PassportCommitInput, domain.PassportDate) (domain.PassportReceipt, error)
	ReadPassportReceipt(context.Context, domain.PassportPolicy, domain.PassportID) (domain.PassportReceipt, error)
	LoadPassportExport(context.Context, domain.PassportPolicy, domain.PassportExportRequest, domain.PassportSnapshotPurpose) (domain.PassportExport, error)
}
