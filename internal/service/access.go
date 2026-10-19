package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type AccessStore interface {
	Accounts(context.Context) ([]domain.Account, error)
	Account(context.Context, int64) (domain.Account, error)
	LoginCredential(context.Context, string) (domain.Account, string, error)
	CreateAccount(context.Context, domain.Account, string, bool) (domain.Account, error)
	UpdateAccount(context.Context, domain.Account, *string) (domain.Account, error)
	CreateSession(context.Context, int64, int64, string, time.Time) error
	SessionAccount(context.Context, string) (domain.Account, error)
	DeleteSession(context.Context, string) error
	AccountForIdentity(context.Context, string, string) (domain.Account, error)
	BindIdentity(context.Context, string, string, int64) error
}
type loginBucket struct {
	since time.Time
	count int
}
type Access struct {
	store    AccessStore
	enabled  bool
	dummy    []byte
	slots    chan struct{}
	mu       sync.Mutex
	attempts map[string]loginBucket
}

func NewAccess(store AccessStore, enabled bool) (*Access, error) {
	a := &Access{store: store, enabled: enabled, slots: make(chan struct{}, 4), attempts: map[string]loginBucket{}}
	if enabled {
		var err error
		a.dummy, err = bcrypt.GenerateFromPassword([]byte("pulse-placeholder-for-login-timing"), 12)
		if err != nil {
			return nil, err
		}
	}
	return a, nil
}
func (a *Access) Enabled() bool { return a != nil && a.enabled }

var accountLogin = regexp.MustCompile(`^[a-z0-9][a-z0-9._@-]{2,79}$`)

func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < 15 || len(password) > 72 {
		return domain.NewError(domain.Invalid, "Пароль должен содержать минимум 15 символов и не превышать 72 байта UTF-8")
	}
	return nil
}
func normalizeAccount(in domain.AccountInput, create bool) (domain.Account, error) {
	in.Login = strings.ToLower(strings.TrimSpace(in.Login))
	in.Name = strings.TrimSpace(in.Name)
	if !accountLogin.MatchString(in.Login) || in.Name == "" || utf8.RuneCountInString(in.Name) > 120 {
		return domain.Account{}, domain.NewError(domain.Invalid, "Нужны корректный логин (3–80 латинских символов) и имя до 120 символов")
	}
	if in.Role != "admin" && in.Role != "technologist" && in.Role != "viewer" {
		return domain.Account{}, domain.NewError(domain.Invalid, "Недопустимая роль")
	}
	if in.Active == nil && !create {
		return domain.Account{}, domain.NewError(domain.Invalid, "Укажите active")
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	if in.Role == "admin" {
		in.Scope = domain.AccessScope{Funds: []string{"operational", "presentation"}}
	}
	if create && in.Scope.Funds == nil {
		in.Scope.Funds = []string{"operational"}
	}
	if len(in.Scope.Funds) == 0 || len(in.Scope.Funds) > 2 {
		return domain.Account{}, domain.NewError(domain.Invalid, "Укажите доступные фонды")
	}
	seen := map[string]bool{}
	for _, fund := range in.Scope.Funds {
		if (fund != "operational" && fund != "presentation") || seen[fund] {
			return domain.Account{}, domain.NewError(domain.Invalid, "Некорректный фонд")
		}
		seen[fund] = true
	}
	for _, scope := range []*[]int64{in.Scope.NGDUs, in.Scope.Departments} {
		if scope != nil && len(*scope) > 1000 {
			return domain.Account{}, domain.NewError(domain.Invalid, "Слишком большой список ограничений")
		}
	}
	return domain.Account{Login: in.Login, Name: in.Name, Role: in.Role, Scope: in.Scope, Active: active, Revision: in.Revision}, nil
}
func requireAccountAdmin(ctx context.Context) error {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || p.Role != "admin" {
		return domain.NewError(domain.Forbidden, "Требуется роль администратора")
	}
	return nil
}
func (a *Access) Bootstrap(ctx context.Context, in domain.AccountInput) (domain.Account, error) {
	if !a.Enabled() {
		return domain.Account{}, domain.NewError(domain.Unavailable, "Персональный вход не включён")
	}
	in.Role = "admin"
	active := true
	in.Active = &active
	return a.create(ctx, in, true)
}
func (a *Access) create(ctx context.Context, in domain.AccountInput, bootstrap bool) (domain.Account, error) {
	user, err := normalizeAccount(in, true)
	if err != nil {
		return user, err
	}
	if in.Password == nil {
		if bootstrap {
			return user, domain.NewError(domain.Invalid, "Укажите пароль первого администратора")
		}
		return a.store.CreateAccount(ctx, user, "", false)
	}
	if err = validatePassword(*in.Password); err != nil {
		return user, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), 12)
	if err != nil {
		return user, err
	}
	return a.store.CreateAccount(ctx, user, string(hash), bootstrap)
}
func (a *Access) Accounts(ctx context.Context) ([]domain.Account, error) {
	if !a.Enabled() {
		return nil, domain.NewError(domain.Unavailable, "Персональный вход не включён")
	}
	if err := requireAccountAdmin(ctx); err != nil {
		return nil, err
	}
	return a.store.Accounts(ctx)
}
func (a *Access) Create(ctx context.Context, in domain.AccountInput) (domain.Account, error) {
	if !a.Enabled() {
		return domain.Account{}, domain.NewError(domain.Unavailable, "Персональный вход не включён")
	}
	if err := requireAccountAdmin(ctx); err != nil {
		return domain.Account{}, err
	}
	return a.create(ctx, in, false)
}
func (a *Access) Update(ctx context.Context, id int64, in domain.AccountInput) (domain.Account, error) {
	if !a.Enabled() {
		return domain.Account{}, domain.NewError(domain.Unavailable, "Персональный вход не включён")
	}
	if err := requireAccountAdmin(ctx); err != nil {
		return domain.Account{}, err
	}
	if in.Revision < 1 {
		return domain.Account{}, domain.NewError(domain.Invalid, "Нужна версия учётной записи")
	}
	user, err := normalizeAccount(in, false)
	if err != nil {
		return user, err
	}
	user.ID = id
	var hash *string
	if in.Password != nil {
		if err = validatePassword(*in.Password); err != nil {
			return user, err
		}
		raw, err := bcrypt.GenerateFromPassword([]byte(*in.Password), 12)
		if err != nil {
			return user, err
		}
		value := string(raw)
		hash = &value
	}
	return a.store.UpdateAccount(ctx, user, hash)
}
func (a *Access) allowLogin(ip, login string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if len(a.attempts) > 10000 {
		for key, bucket := range a.attempts {
			if now.Sub(bucket.since) > time.Minute {
				delete(a.attempts, key)
			}
		}
		if len(a.attempts) > 10000 {
			return false
		}
	}
	for _, key := range []string{ip, ip + "/" + login} {
		bucket := a.attempts[key]
		if now.Sub(bucket.since) > time.Minute {
			bucket = loginBucket{since: now}
		}
		limit := 100
		if key != ip {
			limit = 5
		}
		if bucket.count >= limit {
			return false
		}
		bucket.count++
		a.attempts[key] = bucket
	}
	return true
}
func sessionDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (a *Access) Login(ctx context.Context, login, password, ip string) (domain.Account, string, error) {
	if !a.Enabled() {
		return domain.Account{}, "", domain.NewError(domain.Unavailable, "Персональный вход не включён")
	}
	login = strings.ToLower(strings.TrimSpace(login))
	bucketLogin := login
	if len(bucketLogin) > 80 {
		bucketLogin = bucketLogin[:80]
	}
	if !a.allowLogin(ip, bucketLogin) {
		return domain.Account{}, "", domain.NewError(domain.Limited, "Слишком много попыток. Повторите через минуту")
	}
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		return domain.Account{}, "", domain.NewError(domain.Limited, "Вход занят; повторите немного позже")
	}
	if !accountLogin.MatchString(login) {
		_ = bcrypt.CompareHashAndPassword(a.dummy, []byte(password))
		return domain.Account{}, "", domain.NewError(domain.Unauthorized, "Неверный логин или пароль")
	}
	user, hash, err := a.store.LoginCredential(ctx, login)
	var de *domain.Error
	if err != nil && (!errors.As(err, &de) || de.Kind != domain.Missing) {
		return domain.Account{}, "", err
	}
	actual := []byte(hash)
	if err != nil || !user.Active || hash == "" {
		actual = a.dummy
	}
	compare := bcrypt.CompareHashAndPassword(actual, []byte(password))
	if compare != nil || err != nil || !user.Active || hash == "" || len(password) > 72 {
		return domain.Account{}, "", domain.NewError(domain.Unauthorized, "Неверный логин или пароль")
	}
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		return domain.Account{}, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	if err = a.store.CreateSession(ctx, user.ID, user.Revision, sessionDigest(token), time.Now().Add(8*time.Hour)); err != nil {
		return domain.Account{}, "", err
	}
	return user, token, nil
}
func (a *Access) Identify(ctx context.Context, token string) (domain.Principal, error) {
	if token == "" || len(token) > 128 {
		return domain.Principal{}, domain.NewError(domain.Unauthorized, "Войдите в систему")
	}
	user, err := a.store.SessionAccount(ctx, sessionDigest(token))
	var de *domain.Error
	if errors.As(err, &de) && de.Kind == domain.Missing {
		return domain.Principal{}, domain.NewError(domain.Unauthorized, "Сеанс истёк; войдите снова")
	}
	if err != nil {
		return domain.Principal{}, err
	}
	return domain.AccountPrincipal(user), nil
}
func (a *Access) Logout(ctx context.Context, token string) error {
	if !a.Enabled() {
		return domain.NewError(domain.Unavailable, "Персональный вход не включён")
	}
	return a.store.DeleteSession(ctx, sessionDigest(token))
}

// VerifiedIdentity передаётся только доверенным серверным адаптером после проверки протокола входа.
// Роли и scope не принимаются от клиента или из непроверенных SSO claims.
type VerifiedIdentity struct {
	ProviderID string
	Subject    string
}

func (a *Access) AcceptIdentity(ctx context.Context, identity VerifiedIdentity) (domain.Account, string, error) {
	if !a.Enabled() {
		return domain.Account{}, "", domain.NewError(domain.Unavailable, "Персональный вход не включён")
	}
	user, err := a.store.AccountForIdentity(ctx, identity.ProviderID, identity.Subject)
	var de *domain.Error
	if (errors.As(err, &de) && de.Kind == domain.Missing) || (err == nil && !user.Active) {
		return domain.Account{}, "", domain.NewError(domain.Unauthorized, "Внешняя учётная запись не подключена или отключена")
	}
	if err != nil {
		return domain.Account{}, "", err
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return domain.Account{}, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err = a.store.CreateSession(ctx, user.ID, user.Revision, sessionDigest(token), time.Now().Add(8*time.Hour)); err != nil {
		return domain.Account{}, "", err
	}
	return user, token, nil
}
func (a *Access) BindIdentity(ctx context.Context, provider, subject string, id int64) error {
	if err := requireAccountAdmin(ctx); err != nil {
		return err
	}
	if provider == "" || len(provider) > 80 || subject == "" || len(subject) > 512 {
		return domain.NewError(domain.Invalid, "Некорректная внешняя идентичность")
	}
	return a.store.BindIdentity(ctx, provider, subject, id)
}
