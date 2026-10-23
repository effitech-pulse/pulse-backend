package domain

import (
	"context"
	"slices"
	"strings"
)

// Permission — точный ключ операции; имена ролей не участвуют в авторизации.
type Permission string

type PermissionDefinition struct {
	Key         Permission   `json:"key"`
	Resource    string       `json:"resource"`
	Action      string       `json:"action"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	ScopeKind   string       `json:"scope_kind"`
	Requires    []Permission `json:"requires"`
}

type OrganizationScope struct {
	Kind         string `json:"kind"`
	NGDUID       *int64 `json:"ngdu_id"`
	DepartmentID *int64 `json:"department_id"`
}

type PermissionSource struct {
	RoleID       int64   `json:"role_id"`
	RoleName     string  `json:"role_name"`
	RoleRevision int64   `json:"role_revision"`
	GroupID      *int64  `json:"group_id"`
	GroupName    *string `json:"group_name"`
}

type AuthorizationSnapshot struct {
	Assignments   AccessAssignments                 `json:"assignments"`
	AccountID     int64                             `json:"account_id"`
	Active        bool                              `json:"active"`
	Epoch         int64                             `json:"epoch"`
	GraphRevision int64                             `json:"graph_revision"`
	Permissions   []Permission                      `json:"permissions"`
	Sources       map[Permission][]PermissionSource `json:"sources"`
	Organization  *OrganizationScope                `json:"organization"`
	LegacyScope   AccessScope                       `json:"legacy_scope"`
}

// PermissionCatalog возвращает независимую копию фиксированного каталога.
func PermissionCatalog() []PermissionDefinition {
	out := make([]PermissionDefinition, len(permissionRegistry))
	for i, d := range permissionRegistry {
		out[i] = d
		out[i].Requires = slices.Clone(d.Requires)
	}
	return out
}

var permissionRegistry = buildPermissionRegistry()
var permissionDefinitions = func() map[Permission]PermissionDefinition {
	out := make(map[Permission]PermissionDefinition, len(permissionRegistry))
	for _, d := range permissionRegistry {
		out[d.Key] = d
	}
	return out
}()

func buildPermissionRegistry() []PermissionDefinition {
	var out []PermissionDefinition
	add := func(keys, requires, scope, name string) {
		for _, key := range strings.Fields(keys) {
			resource, action, _ := strings.Cut(key, ".")
			deps := make([]Permission, 0)
			for _, req := range strings.Fields(requires) {
				deps = append(deps, Permission(req))
			}
			out = append(out, PermissionDefinition{Key: Permission(key), Resource: resource, Action: action, Name: name + " · " + action, Description: name + ": операция " + action, ScopeKind: scope, Requires: deps})
		}
	}
	add("wells.read", "", "organization", "Скважины")
	add("wells.create wells.update wells.import wells.export wells.archive wells.restore wells.delete wells.history.read", "wells.read", "organization", "Скважины")
	add("telemetry.read", "wells.read", "organization", "Телеметрия")
	add("telemetry.import telemetry.compat.read", "telemetry.read", "organization", "Телеметрия")
	add("telemetry.compat.score", "telemetry.compat.read checks.run", "organization", "Проверка телеметрии по внешнему идентификатору")
	add("risks.read", "wells.read", "organization", "Риски")
	add("risks.explain risks.export risks.external.read", "risks.read", "organization", "Риски")
	add("checks.read", "wells.read", "organization", "Проверки")
	add("checks.run", "checks.read telemetry.read", "organization", "Запуск проверки")
	add("checks.review", "checks.read", "organization", "Решение по проверке")
	add("filechecks.read", "wells.read", "organization", "Файловые проверки")
	add("filechecks.run filechecks.cancel filechecks.review", "filechecks.read", "organization", "Файловые проверки")
	add("filechecks.legacy.run", "wells.read", "global", "Синхронная файловая проверка")
	add("alerts.read", "risks.read", "organization", "Тревоги")
	add("alerts.review", "alerts.read", "organization", "Решение по тревоге")
	add("incidents.read", "wells.read", "organization", "Инциденты")
	add("incidents.create incidents.delete", "incidents.read", "organization", "Инциденты")
	add("references.read parameters.read", "", "organization", "Справочники")
	add("references.create references.update references.delete", "references.read", "global", "Изменение справочников")
	add("runs.read", "", "organization", "Прогоны")
	add("runs.export", "runs.read", "organization", "Экспорт прогонов")
	add("runs.start", "runs.read risks.read", "global", "Запуск прогона")
	add("cohort.runs.read", "risks.read", "organization", "Прогоны фонда")
	add("services.read metrics.read analytics.read audit.read presentation.scenario.read", "", "global", "Системные данные")
	add("integrations.read", "", "organization", "Интеграции")
	add("integrations.create integrations.update integrations.check integrations.status", "integrations.read", "global", "Управление интеграциями")
	add("integrations.logs.read", "integrations.read", "organization", "Журнал интеграций")
	add("integrations.collect integrations.ingest", "integrations.read telemetry.import", "organization", "Сбор телеметрии")
	add("collectionjobs.read", "integrations.read wells.read", "organization", "Задания сбора")
	add("collectionjobs.run", "collectionjobs.read integrations.collect", "organization", "Запуск задания сбора")
	add("collectionjobs.cancel", "collectionjobs.read", "organization", "Отмена задания сбора")
	add("cohortjobs.read", "cohort.runs.read", "organization", "Задания проверки фонда")
	add("cohortjobs.run", "cohortjobs.read runs.start", "global", "Запуск проверки фонда")
	add("cohortjobs.cancel", "cohortjobs.read", "organization", "Отмена проверки фонда")
	add("users.read", "", "global", "Пользователи")
	add("users.update", "users.read", "global", "Изменение пользователя")
	add("users.scope.assign", "users.update", "global", "Назначение подразделения")
	add("users.create", "users.read users.scope.assign", "global", "Создание пользователя")
	add("roles.read groups.read", "", "global", "Назначаемые роли и группы")
	add("users.roles.assign", "users.update roles.read", "global", "Назначение ролей")
	add("users.groups.assign", "users.update groups.read", "global", "Назначение групп")
	add("roles.manage", "roles.read access.manage_global", "global", "Управление ролями")
	add("groups.manage", "groups.read access.manage_global", "global", "Управление группами")
	add("access.manage_global", "", "global", "Глобальное управление доступом")
	slices.SortFunc(out, func(a, b PermissionDefinition) int {
		if c := strings.Compare(a.Resource, b.Resource); c != 0 {
			return c
		}
		return strings.Compare(string(a.Key), string(b.Key))
	})
	return out
}

func (a AuthorizationSnapshot) valid() bool {
	if !a.Active || a.Organization != nil && !a.Organization.valid() {
		return false
	}
	for _, key := range a.Permissions {
		if _, ok := permissionDefinitions[key]; !ok {
			return false
		}
	}
	return true
}

// Require проверяет весь граф предпосылок после union, не дополняя выданный набор.
func (a AuthorizationSnapshot) Require(keys ...Permission) error {
	if !a.valid() {
		return NewError(Forbidden, "invalid authorization snapshot")
	}
	granted := make(map[Permission]bool, len(a.Permissions))
	for _, key := range a.Permissions {
		granted[key] = true
	}
	checked := make(map[Permission]bool)
	var allows func(Permission) bool
	allows = func(key Permission) bool {
		d, known := permissionDefinitions[key]
		if !known || !granted[key] {
			return false
		}
		if checked[key] {
			return true
		}
		for _, req := range d.Requires {
			if !allows(req) {
				return false
			}
		}
		checked[key] = true
		return true
	}
	for _, key := range keys {
		if !allows(key) {
			return NewError(Forbidden, "permission denied: "+string(key))
		}
	}
	return nil
}

// RequirePermissions — fail-closed точка входа для новых enforcement callers.
func RequirePermissions(ctx context.Context, keys ...Permission) error {
	if ctx == nil {
		return NewError(Forbidden, "authorization required")
	}
	p, ok := CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.Authorization == nil {
		return NewError(Forbidden, "authorization required")
	}
	return p.Authorization.Require(keys...)
}

func (s OrganizationScope) valid() bool {
	switch s.Kind {
	case "none", "organization":
		return s.NGDUID == nil && s.DepartmentID == nil
	case "ngdu":
		return s.NGDUID != nil && s.DepartmentID == nil
	case "department":
		return s.NGDUID != nil && s.DepartmentID != nil
	default:
		return false
	}
}

// AllowsWell проверяет только область данных; permission проверяет вызывающий код.
func (a AuthorizationSnapshot) AllowsWell(w Well) bool {
	if !a.valid() {
		return false
	}
	fund := w.FundType
	if fund == "" {
		fund = "operational"
	}
	if !slices.Contains(a.LegacyScope.Funds, fund) {
		return false
	}
	if a.Organization == nil {
		return (a.LegacyScope.NGDUs == nil || slices.Contains(*a.LegacyScope.NGDUs, w.NGDU)) && (a.LegacyScope.Departments == nil || slices.Contains(*a.LegacyScope.Departments, w.Department))
	}
	switch a.Organization.Kind {
	case "organization":
		return true
	case "ngdu":
		return *a.Organization.NGDUID == w.NGDU
	case "department":
		return *a.Organization.NGDUID == w.NGDU && *a.Organization.DepartmentID == w.Department
	default:
		return false
	}
}

// CanGrant не позволяет выдать отсутствующий у управляющего ключ, включая global manager.
func (a AuthorizationSnapshot) CanGrant(keys []Permission) bool { return a.Require(keys...) == nil }

// CanManageAccount проверяет границу управления; action permissions проверяются отдельно.
// Active target намеренно не требуется: блокировка не должна делать аккаунт неуправляемым.
func (a AuthorizationSnapshot) CanManageAccount(target AuthorizationSnapshot) bool {
	if !a.valid() {
		return false
	}
	if a.Require("access.manage_global") == nil {
		return true
	}
	if a.Organization == nil || target.Organization == nil || !a.Organization.contains(*target.Organization) {
		return false
	}
	for _, fund := range target.LegacyScope.Funds {
		if !slices.Contains(a.LegacyScope.Funds, fund) {
			return false
		}
	}
	target.Active = true
	if !target.valid() {
		return false
	}
	for _, key := range target.Permissions {
		if target.Require(key) == nil && a.Require(key) != nil {
			return false
		}
	}
	return true
}

func (s OrganizationScope) contains(target OrganizationScope) bool {
	if !s.valid() || !target.valid() || s.Kind == "none" || target.Kind == "none" {
		return false
	}
	if s.Kind == "organization" {
		return true
	}
	if target.Kind == "organization" || *s.NGDUID != *target.NGDUID {
		return false
	}
	return s.Kind == "ngdu" || target.Kind == "department" && *s.DepartmentID == *target.DepartmentID
}

// RolePermissionGrant сохраняет различие прямого и группового источника роли.
type RolePermissionGrant struct {
	Role      AccessRole
	GroupID   *int64
	GroupName *string
}

// ResolveRolePermissions объединяет только явно выданные ключи и все distinct источники.
// Неизвестные ключи сохраняются, чтобы Require запретил некорректный снимок целиком.
func ResolveRolePermissions(grants []RolePermissionGrant) ([]Permission, map[Permission][]PermissionSource) {
	sources := make(map[Permission][]PermissionSource)
	for _, grant := range grants {
		source := PermissionSource{RoleID: grant.Role.ID, RoleName: grant.Role.Name, RoleRevision: grant.Role.Revision, GroupID: copyAccessPointer(grant.GroupID), GroupName: copyAccessPointer(grant.GroupName)}
		for _, key := range grant.Role.PermissionKeys {
			if !slices.ContainsFunc(sources[key], func(v PermissionSource) bool { return comparePermissionSource(v, source) == 0 }) {
				sources[key] = append(sources[key], source)
			}
		}
	}
	keys := make([]Permission, 0, len(sources))
	for key := range sources {
		keys = append(keys, key)
		slices.SortFunc(sources[key], comparePermissionSource)
	}
	slices.Sort(keys)
	return keys, sources
}

func copyAccessPointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	value := *p
	return &value
}
func compareAccessPointer[T ~int64 | ~string](a, b *T) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return -1
	}
	if b == nil {
		return 1
	}
	if *a < *b {
		return -1
	}
	if *a > *b {
		return 1
	}
	return 0
}
func comparePermissionSource(a, b PermissionSource) int {
	if c := compareAccessPointer(a.GroupID, b.GroupID); c != 0 {
		return c
	}
	if a.RoleID < b.RoleID {
		return -1
	}
	if a.RoleID > b.RoleID {
		return 1
	}
	if a.RoleRevision < b.RoleRevision {
		return -1
	}
	if a.RoleRevision > b.RoleRevision {
		return 1
	}
	if c := strings.Compare(a.RoleName, b.RoleName); c != 0 {
		return c
	}
	return compareAccessPointer(a.GroupName, b.GroupName)
}

// FullApplicationScope доказывает полную область, включая оба технических фонда.
// Управление аккаунтами и имя роли не участвуют в этом доказательстве.
func (a AuthorizationSnapshot) FullApplicationScope() bool {
	if !a.valid() || !slices.Contains(a.LegacyScope.Funds, "operational") || !slices.Contains(a.LegacyScope.Funds, "presentation") {
		return false
	}
	if a.Organization != nil {
		return a.Organization.Kind == "organization"
	}
	return a.LegacyScope.NGDUs == nil && a.LegacyScope.Departments == nil
}

// TrustedLegacyPrincipal вызывается только после проверки server-owned legacy профиля.
func TrustedLegacyPrincipal(name string) Principal {
	a := AuthorizationSnapshot{Active: true, Permissions: BuiltinAccessPermissions("admin"), Organization: &OrganizationScope{Kind: "organization"}, LegacyScope: AccessScope{Funds: []string{"operational", "presentation"}}}
	return Principal{trustedCompatibility: true, Name: name, Role: "admin", Scope: a.LegacyScope, Authorization: &a, Authenticated: true, Mechanism: "legacy"}
}

// TrustedCompatibilityPrincipal адаптирует проверенный server-side legacy account.
// Вызывающий обязан проверить credential/account; клиентский role сюда не передаётся.
func TrustedCompatibilityPrincipal(p Principal) Principal {
	a := AuthorizationSnapshot{Active: true}
	if p.Authorization != nil {
		a = *p.Authorization
	}
	a.Permissions = BuiltinAccessPermissions(p.Role)
	a.LegacyScope = p.Scope
	a.Organization = nil
	if p.AccountID != nil {
		a.AccountID = *p.AccountID
	}
	if p.Role == "admin" {
		a.LegacyScope = AccessScope{Funds: []string{"operational", "presentation"}}
		a.Organization = &OrganizationScope{Kind: "organization"}
	}
	p.Authenticated = true
	p.trustedCompatibility = true
	p.Authorization = &a
	return p
}

// WithSystemPrincipal назначает фиксированный server-owned actor внутреннему scheduler.
func WithSystemPrincipal(ctx context.Context) context.Context {
	p := TrustedLegacyPrincipal("Планировщик")
	p.Mechanism = "system"
	return WithPrincipal(ctx, p)
}
