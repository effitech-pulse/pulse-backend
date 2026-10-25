package postgres

import (
	"context"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// ValidateEquipmentPassports checks schema readiness without applying migrations or grants.
func (r *Repository) ValidateEquipmentPassports(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var ready bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM backend_schema_migration WHERE version=18)
 AND to_regclass('equipment_passport') IS NOT NULL
 AND to_regclass('equipment_passport_component') IS NOT NULL
 AND to_regclass('equipment_passport_installation') IS NOT NULL
 AND to_regclass('equipment_passport_event') IS NOT NULL
 AND to_regclass('equipment_passport_preview') IS NOT NULL
 AND to_regclass('equipment_passport_receipt') IS NOT NULL
 AND to_regclass('equipment_passport_enrollment') IS NOT NULL`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return domain.NewError(domain.Unavailable, "equipment_passports_schema_required")
	}
	return nil
}
