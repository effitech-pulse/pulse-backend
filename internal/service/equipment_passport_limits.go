package service

import (
	"context"
	"sync"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Общий process gate, включая разные instances EquipmentPassports.
var passportWork = struct {
	sync.Mutex
	actors map[int64]bool
	count  int
}{actors: make(map[int64]bool)}

func passportWorkContext(ctx context.Context) (context.Context, func(), error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || p.AccountID == nil {
		return nil, nil, domain.NewError(domain.Unauthorized, "Требуется вход")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	passportWork.Lock()
	id := *p.AccountID
	if passportWork.actors[id] || passportWork.count >= 2 {
		passportWork.Unlock()
		return nil, nil, domain.NewError(domain.Limited, "equipment_passport_busy")
	}
	passportWork.actors[id] = true
	passportWork.count++
	passportWork.Unlock()
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	release := func() {
		cancel()
		passportWork.Lock()
		delete(passportWork.actors, id)
		passportWork.count--
		passportWork.Unlock()
	}
	return bounded, release, nil
}
