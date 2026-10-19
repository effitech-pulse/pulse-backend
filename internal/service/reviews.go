package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type ReviewStore interface {
	ReviewTarget(context.Context, string, string) (domain.ReviewTarget, error)
	Review(context.Context, domain.ReviewTarget, int64) (domain.Review, error)
	CommitReview(context.Context, domain.ReviewTarget, domain.ReviewInput, domain.ReviewActor, string, string) (domain.Review, error)
}
type Reviews struct {
	scope   FundScope
	store   ReviewStore
	catalog Store
}

func (s *Reviews) WithCatalog(c Store) *Reviews { s.catalog = c; return s }

func (s *Reviews) WithScope(scope FundScope) *Reviews { s.scope = scope; return s }

func NewReviews(store ReviewStore) *Reviews { return &Reviews{store: store} }
func (s *Reviews) target(ctx context.Context, kind, id string, well *int64) (domain.ReviewTarget, error) {
	if kind != "check" && kind != "alert" {
		return domain.ReviewTarget{}, domain.NewError(domain.Invalid, "Неизвестный тип результата")
	}
	target, err := s.store.ReviewTarget(ctx, kind, id)
	if err != nil {
		return target, err
	}
	if s.scope != nil {
		if target.WellID == nil {
			return target, domain.NewError(domain.Missing, "Результат не найден")
		}
		if _, err = s.scope.RequireWell(ctx, *target.WellID); err != nil {
			return target, err
		}
	}
	if well != nil && (target.WellID == nil || *well != *target.WellID) {
		return target, domain.NewError(domain.Missing, "Проверка не найдена у этой скважины")
	}
	if p, ok := domain.CurrentPrincipal(ctx); ok && p.Role != "admin" {
		if target.WellID == nil || s.catalog == nil {
			return target, domain.NewError(domain.Missing, "Результат не найден")
		}
		w, err := s.catalog.Well(ctx, *target.WellID)
		if err != nil {
			return target, err
		}
		if !p.Allows(w) {
			return target, domain.NewError(domain.Missing, "Результат не найден")
		}
	}
	target.Initial = domain.ReviewState{Status: "new"}
	switch target.LegacyStatus {
	case "acknowledged":
		target.Initial.Status = "in_progress"
	case "false_positive", "confirmed", "inconclusive":
		target.Initial.Status = "closed"
		target.Initial.Conclusion = target.LegacyStatus
		if target.LegacyStatus == "false_positive" {
			target.Initial.Conclusion = "not_confirmed"
		}
	}
	return target, nil
}
func (s *Reviews) Get(ctx context.Context, kind, id string, well *int64, before int64) (domain.Review, error) {
	target, err := s.target(ctx, kind, id, well)
	if err != nil {
		return domain.Review{}, err
	}
	return s.store.Review(ctx, target, before)
}
func legacyReviewStatus(in domain.ReviewInput) string {
	if in.Status == "new" {
		return "new"
	}
	if in.Status == "closed" {
		if in.Conclusion == "not_confirmed" {
			return "false_positive"
		}
		return in.Conclusion
	}
	return "acknowledged"
}
func reviewFingerprint(in domain.ReviewInput) (string, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
func (s *Reviews) Update(ctx context.Context, kind, id string, well *int64, in domain.ReviewInput, actor domain.ReviewActor) (domain.Review, error) {
	in.Owner = strings.TrimSpace(in.Owner)
	in.Comment = strings.TrimSpace(in.Comment)
	if !checkRequestID.MatchString(in.RequestID) || in.Revision < 0 || utf8.RuneCountInString(in.Owner) > 120 || utf8.RuneCountInString(in.Comment) > 4000 {
		return domain.Review{}, domain.NewError(domain.Invalid, "Некорректный ключ, версия или длина полей")
	}
	if in.Status != "new" && in.Status != "in_progress" && in.Status != "closed" {
		return domain.Review{}, domain.NewError(domain.Invalid, "Недопустимый статус обработки")
	}
	if in.Status != "new" && in.Owner == "" {
		return domain.Review{}, domain.NewError(domain.Invalid, "Укажите ответственного")
	}
	if in.Status == "closed" {
		if (in.Conclusion != "confirmed" && in.Conclusion != "not_confirmed" && in.Conclusion != "inconclusive") || in.Comment == "" {
			return domain.Review{}, domain.NewError(domain.Invalid, "Для заключения нужны результат и комментарий технолога")
		}
	} else if in.Conclusion != "" {
		return domain.Review{}, domain.NewError(domain.Invalid, "Заключение допускается при завершении обработки")
	}
	target, err := s.target(ctx, kind, id, well)
	if err != nil {
		return domain.Review{}, err
	}
	current, err := s.store.Review(ctx, target, 0)
	if err != nil {
		return domain.Review{}, err
	}
	if current.State.Revision == in.Revision && current.State.Status == "closed" && in.Status != "closed" && in.Comment == "" {
		return domain.Review{}, domain.NewError(domain.Invalid, "При повторном открытии укажите причину")
	}
	fingerprint, err := reviewFingerprint(in)
	if err != nil {
		return domain.Review{}, err
	}
	return s.store.CommitReview(ctx, target, in, actor, fingerprint, legacyReviewStatus(in))
}

// LegacyAlert сохраняет совместимый PATCH и включает его изменения в ту же историю.
func (s *Reviews) LegacyAlert(ctx context.Context, id, status string, actor domain.ReviewActor) (domain.Review, error) {
	if status != "new" && status != "acknowledged" && status != "false_positive" {
		return domain.Review{}, domain.NewError(domain.Invalid, "Недопустимый статус")
	}
	target, err := s.target(ctx, "alert", id, nil)
	if err != nil {
		return domain.Review{}, err
	}
	current, err := s.store.Review(ctx, target, 0)
	if err != nil {
		return domain.Review{}, err
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return domain.Review{}, err
	}
	in := domain.ReviewInput{RequestID: hex.EncodeToString(token), Revision: current.State.Revision, Status: "new", Owner: current.State.Owner, Comment: "Статус изменён через совместимый API"}
	if status == "acknowledged" {
		in.Status = "in_progress"
	}
	if status == "false_positive" {
		in.Status = "closed"
		in.Conclusion = "not_confirmed"
	}
	fingerprint, err := reviewFingerprint(in)
	if err != nil {
		return domain.Review{}, err
	}
	return s.store.CommitReview(ctx, target, in, actor, fingerprint, status)
}
