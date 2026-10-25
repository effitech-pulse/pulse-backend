package service

import (
	"context"
	"io"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// NotificationStore проверяет текущие права, scope и границу курсора в одной
// транзакции до выборки страницы, подсчёта или персональной записи прочтения.
// Читатели целей возвращают исходную тревогу и безопасную проекцию источника.
type NotificationStore interface {
	NotificationCapabilities(context.Context, domain.NotificationScope) (domain.NotificationCapabilities, error)
	ReadNotificationPage(context.Context, domain.NotificationQuery) (domain.NotificationRecords, error)
	CountUnreadNotifications(context.Context, domain.NotificationScope) (domain.NotificationCount, error)
	MarkNotificationRead(context.Context, domain.NotificationScope, int64) (domain.NotificationRead, error)
	ReadNotificationAlert(context.Context, domain.NotificationScope, domain.NotificationAlertLocator) (domain.NotificationAlertContext, error)
	ReadNotificationSource(context.Context, domain.NotificationScope, string) (domain.NotificationSourceOverview, error)
}

// NotificationsOptions настраивает выдачу; UIEnabled не управляет capture.
// Now и Random позволяют проверять срок курсора и перезапуск без ожиданий.
type NotificationsOptions struct {
	AuthMode  string
	Mode      string
	Prefix    string
	UIEnabled bool
	Now       func() time.Time
	Random    io.Reader
}
