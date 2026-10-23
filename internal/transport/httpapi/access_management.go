package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// decimal is intentionally string-only on input: JSON numbers may already be rounded by clients.
type decimal int64

func (n decimal) MarshalJSON() ([]byte, error) { return json.Marshal(strconv.FormatInt(int64(n), 10)) }
func (n *decimal) UnmarshalJSON(raw []byte) error {
	var s string
	if e := json.Unmarshal(raw, &s); e != nil {
		return fmt.Errorf("decimal string required")
	}
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil || strconv.FormatInt(v, 10) != s {
		return fmt.Errorf("invalid decimal string")
	}
	*n = decimal(v)
	return nil
}
func decimals(v []int64) []decimal {
	out := make([]decimal, 0, len(v))
	for _, id := range v {
		out = append(out, decimal(id))
	}
	return out
}
func integers(v []decimal) []int64 {
	out := make([]int64, 0, len(v))
	for _, id := range v {
		out = append(out, int64(id))
	}
	return out
}
func decimalPtr(v *int64) *decimal {
	if v == nil {
		return nil
	}
	n := decimal(*v)
	return &n
}
func integerPtr(v *decimal) *int64 {
	if v == nil {
		return nil
	}
	n := int64(*v)
	return &n
}

type organizationDTO struct {
	Kind         string   `json:"kind"`
	NGDUID       *decimal `json:"ngdu_id"`
	DepartmentID *decimal `json:"department_id"`
}

func organization(v *domain.OrganizationScope) *organizationDTO {
	if v == nil {
		return nil
	}
	return &organizationDTO{v.Kind, decimalPtr(v.NGDUID), decimalPtr(v.DepartmentID)}
}
func (v organizationDTO) value() domain.OrganizationScope {
	return domain.OrganizationScope{Kind: v.Kind, NGDUID: integerPtr(v.NGDUID), DepartmentID: integerPtr(v.DepartmentID)}
}

type roleSummaryDTO struct {
	ID         decimal `json:"id"`
	Name       string  `json:"name"`
	Revision   decimal `json:"revision"`
	BuiltinKey *string `json:"builtin_key"`
}
type groupMembershipDTO struct {
	ID       decimal   `json:"id"`
	Name     string    `json:"name"`
	Revision decimal   `json:"revision"`
	RoleIDs  []decimal `json:"role_ids"`
}
type permissionSourceDTO struct {
	RoleID       decimal  `json:"role_id"`
	RoleName     string   `json:"role_name"`
	RoleRevision decimal  `json:"role_revision"`
	BuiltinKey   *string  `json:"builtin_key"`
	GroupID      *decimal `json:"group_id"`
	GroupName    *string  `json:"group_name"`
}
type accessFields struct {
	AccessID          decimal                                     `json:"access_id"`
	AccessRevision    decimal                                     `json:"access_revision"`
	AccessModel       string                                      `json:"access_model"`
	LegacyRole        *string                                     `json:"legacy_role"`
	RoleIDs           []decimal                                   `json:"role_ids"`
	GroupIDs          []decimal                                   `json:"group_ids"`
	Roles             []roleSummaryDTO                            `json:"roles"`
	DirectRoles       []roleSummaryDTO                            `json:"direct_roles"`
	Groups            []groupMembershipDTO                        `json:"groups"`
	PermissionKeys    []domain.Permission                         `json:"permission_keys"`
	PermissionSources map[domain.Permission][]permissionSourceDTO `json:"permission_sources"`
	OrganizationScope *organizationDTO                            `json:"organization_scope"`
	LegacyScope       bool                                        `json:"legacy_scope"`
	AuthzEpoch        decimal                                     `json:"authz_epoch"`
	GraphRevision     decimal                                     `json:"graph_revision"`
}

func viewFields(v domain.AccountAccessView, enforce bool) accessFields {
	a := v.Authorization
	f := accessFields{AccessID: decimal(v.Account.ID), AccessRevision: decimal(v.Account.Revision), AccessModel: "legacy", LegacyRole: domain.LegacyAccessRole(a), RoleIDs: decimals(a.Assignments.DirectRoleIDs), GroupIDs: []decimal{}, Roles: []roleSummaryDTO{}, DirectRoles: []roleSummaryDTO{}, Groups: []groupMembershipDTO{}, PermissionKeys: append([]domain.Permission{}, a.Permissions...), PermissionSources: map[domain.Permission][]permissionSourceDTO{}, OrganizationScope: organization(a.Organization), LegacyScope: a.Organization == nil, AuthzEpoch: decimal(a.Epoch), GraphRevision: decimal(a.GraphRevision)}
	if enforce {
		f.AccessModel = "rbac_v1"
	}
	roles := map[int64]roleSummaryDTO{}
	for _, r := range a.Assignments.Roles {
		d := roleSummaryDTO{decimal(r.ID), r.Name, decimal(r.Revision), r.BuiltinKey}
		roles[r.ID] = d
		f.Roles = append(f.Roles, d)
	}
	for _, id := range a.Assignments.DirectRoleIDs {
		f.DirectRoles = append(f.DirectRoles, roles[id])
	}
	for _, g := range a.Assignments.Groups {
		f.GroupIDs = append(f.GroupIDs, decimal(g.ID))
		f.Groups = append(f.Groups, groupMembershipDTO{decimal(g.ID), g.Name, decimal(g.Revision), decimals(g.RoleIDs)})
	}
	for k, sources := range a.Sources {
		out := []permissionSourceDTO{}
		for _, s := range sources {
			out = append(out, permissionSourceDTO{decimal(s.RoleID), s.RoleName, decimal(s.RoleRevision), roles[s.RoleID].BuiltinKey, decimalPtr(s.GroupID), s.GroupName})
		}
		f.PermissionSources[k] = out
	}
	return f
}

type accountDTO struct {
	domain.Account
	accessFields
	Role string `json:"role"`
}

func accountViewDTO(v domain.AccountAccessView, enforce bool) accountDTO {
	role := v.Account.Role
	if enforce {
		role = "viewer"
		if key := domain.LegacyAccessRole(v.Authorization); key != nil {
			role = *key
		}
	}
	return accountDTO{v.Account, viewFields(v, enforce), role}
}

type principalDTO struct {
	AccountID     *int64             `json:"account_id"`
	Name          string             `json:"name"`
	Role          string             `json:"role"`
	Scope         domain.AccessScope `json:"scope"`
	Authenticated bool               `json:"authenticated"`
	Mechanism     string             `json:"mechanism"`
	accessFields
}

func principalViewDTO(v domain.AccountAccessView, enforce bool) principalDTO {
	a := accountViewDTO(v, enforce)
	return principalDTO{&v.Account.ID, v.Account.Name, a.Role, v.Account.Scope, true, "session", a.accessFields}
}

type roleDTO struct {
	roleSummaryDTO
	Description    string              `json:"description"`
	PermissionKeys []domain.Permission `json:"permission_keys"`
}

func accessRoleDTO(v domain.AccessRole) roleDTO {
	return roleDTO{roleSummaryDTO{decimal(v.ID), v.Name, decimal(v.Revision), v.BuiltinKey}, v.Description, append([]domain.Permission{}, v.PermissionKeys...)}
}

type groupDTO struct {
	ID          decimal   `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Revision    decimal   `json:"revision"`
	RoleIDs     []decimal `json:"role_ids"`
}

func accessGroupDTO(v domain.AccessGroup) groupDTO {
	return groupDTO{decimal(v.ID), v.Name, v.Description, decimal(v.Revision), decimals(v.RoleIDs)}
}

type accessPageDTO[T any] struct {
	Items         []T     `json:"items"`
	NextCursor    *string `json:"next_cursor"`
	TotalVisible  int     `json:"total_visible"`
	GraphRevision decimal `json:"graph_revision"`
}

func accessQuery(c *gin.Context) (domain.AccessPageQuery, error) {
	n, e := limit(c, 50, 100)
	if e != nil {
		return domain.AccessPageQuery{}, e
	}
	q := domain.AccessPageQuery{Search: c.Query("search"), Limit: n, Cursor: c.Query("cursor")}
	if s := c.Query("graph_revision"); s != "" {
		v, e := strconv.ParseInt(s, 10, 64)
		if e != nil || v < 1 || strconv.FormatInt(v, 10) != s {
			return q, validation("Некорректная версия графа")
		}
		q.GraphRevision = &v
	}
	return q, nil
}
func (s *Server) authAccount(c *gin.Context) error {
	n, e := id(c, "user_id")
	if e != nil {
		return e
	}
	v, e := s.Access.AccountView(c.Request.Context(), n)
	if e != nil {
		return e
	}
	return writeJSON(c, 200, accountViewDTO(v, s.Access.RBACEnforced()))
}
func (s *Server) authPermissions(c *gin.Context) error {
	return writeJSON(c, 200, domain.PermissionCatalog())
}
func (s *Server) authRoles(c *gin.Context) error {
	store, actor, e := s.Access.ManagementStore(c.Request.Context())
	if e != nil {
		return e
	}
	if c.Param("role_id") != "" {
		id, e := id(c, "role_id")
		if e != nil {
			return e
		}
		v, e := store.AccessRole(c.Request.Context(), actor, id)
		if e != nil {
			return e
		}
		return writeJSON(c, 200, accessRoleDTO(v))
	}
	q, e := accessQuery(c)
	if e != nil {
		return e
	}
	v, e := store.ListAccessRoles(c.Request.Context(), actor, q)
	if e != nil {
		return e
	}
	out := accessPageDTO[roleDTO]{Items: []roleDTO{}, NextCursor: v.NextCursor, TotalVisible: v.TotalVisible, GraphRevision: decimal(v.GraphRevision)}
	for _, r := range v.Items {
		out.Items = append(out.Items, accessRoleDTO(r))
	}
	return writeJSON(c, 200, out)
}
func (s *Server) authGroups(c *gin.Context) error {
	store, actor, e := s.Access.ManagementStore(c.Request.Context())
	if e != nil {
		return e
	}
	if c.Param("group_id") != "" {
		id, e := id(c, "group_id")
		if e != nil {
			return e
		}
		v, e := store.AccessGroup(c.Request.Context(), actor, id)
		if e != nil {
			return e
		}
		return writeJSON(c, 200, accessGroupDTO(v))
	}
	q, e := accessQuery(c)
	if e != nil {
		return e
	}
	v, e := store.ListAccessGroups(c.Request.Context(), actor, q)
	if e != nil {
		return e
	}
	out := accessPageDTO[groupDTO]{Items: []groupDTO{}, NextCursor: v.NextCursor, TotalVisible: v.TotalVisible, GraphRevision: decimal(v.GraphRevision)}
	for _, g := range v.Items {
		out.Items = append(out.Items, accessGroupDTO(g))
	}
	return writeJSON(c, 200, out)
}
func (s *Server) authMembers(c *gin.Context) error {
	gid, e := id(c, "group_id")
	if e != nil {
		return e
	}
	q, e := accessQuery(c)
	if e != nil {
		return e
	}
	page, e := s.Access.GroupMemberViews(c.Request.Context(), gid, q)
	if e != nil {
		return e
	}
	out := accessPageDTO[accountDTO]{Items: []accountDTO{}, NextCursor: page.NextCursor, TotalVisible: page.TotalVisible, GraphRevision: decimal(page.GraphRevision)}
	for _, v := range page.Items {
		out.Items = append(out.Items, accountViewDTO(v, s.Access.RBACEnforced()))
	}
	return writeJSON(c, 200, out)
}
func (s *Server) authOrganizations(c *gin.Context) error {
	store, actor, e := s.Access.ManagementStore(c.Request.Context())
	if e != nil {
		return e
	}
	v, e := store.OrganizationOptions(c.Request.Context(), actor)
	if e != nil {
		return e
	}
	type ref struct {
		ID   decimal `json:"id"`
		Name string  `json:"name"`
	}
	type pair struct {
		NGDUID       decimal `json:"ngdu_id"`
		DepartmentID decimal `json:"department_id"`
	}
	out := struct {
		NGDUs       []ref            `json:"ngdus"`
		Departments []ref            `json:"departments"`
		Pairs       []pair           `json:"pairs"`
		ActorScope  *organizationDTO `json:"actor_scope"`
	}{NGDUs: []ref{}, Departments: []ref{}, Pairs: []pair{}, ActorScope: organization(v.ActorScope)}
	for _, r := range v.NGDUs {
		out.NGDUs = append(out.NGDUs, ref{decimal(r.ID), r.Name})
	}
	for _, r := range v.Departments {
		out.Departments = append(out.Departments, ref{decimal(r.ID), r.Name})
	}
	for _, p := range v.Pairs {
		out.Pairs = append(out.Pairs, pair{decimal(p.NGDUID), decimal(p.DepartmentID)})
	}
	return writeJSON(c, 200, out)
}

type userChangeDTO struct {
	ID            *decimal                      `json:"id"`
	Login         string                        `json:"login"`
	Name          string                        `json:"name"`
	Active        bool                          `json:"active"`
	Revision      decimal                       `json:"revision"`
	RoleIDs       []decimal                     `json:"role_ids"`
	GroupIDs      []decimal                     `json:"group_ids"`
	Organization  domain.Field[organizationDTO] `json:"organization"`
	ResetPassword bool                          `json:"reset_password"`
}
type roleChangeDTO struct {
	ID             *decimal            `json:"id"`
	Revision       decimal             `json:"revision"`
	Name           string              `json:"name"`
	Description    string              `json:"description"`
	PermissionKeys []domain.Permission `json:"permission_keys"`
	Delete         bool                `json:"delete"`
}
type groupChangeDTO struct {
	ID          *decimal  `json:"id"`
	Revision    decimal   `json:"revision"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	RoleIDs     []decimal `json:"role_ids"`
	MemberIDs   []decimal `json:"member_ids"`
	Delete      bool      `json:"delete"`
}
type changeDTO struct {
	User  *userChangeDTO  `json:"user"`
	Role  *roleChangeDTO  `json:"role"`
	Group *groupChangeDTO `json:"group"`
}

func (c changeDTO) value() domain.AccessChange {
	out := domain.AccessChange{}
	if u := c.User; u != nil {
		org := domain.Field[domain.OrganizationScope]{Present: u.Organization.Present, Null: u.Organization.Null, Value: u.Organization.Value.value()}
		out.User = &domain.UserAccessChange{ID: integerPtr(u.ID), Login: u.Login, Name: u.Name, Active: u.Active, Revision: int64(u.Revision), RoleIDs: integers(u.RoleIDs), GroupIDs: integers(u.GroupIDs), Organization: org, ResetPassword: u.ResetPassword}
	}
	if r := c.Role; r != nil {
		out.Role = &domain.RoleAccessChange{ID: integerPtr(r.ID), Revision: int64(r.Revision), Name: r.Name, Description: r.Description, PermissionKeys: r.PermissionKeys, Delete: r.Delete}
	}
	if g := c.Group; g != nil {
		out.Group = &domain.GroupAccessChange{ID: integerPtr(g.ID), Revision: int64(g.Revision), Name: g.Name, Description: g.Description, RoleIDs: integers(g.RoleIDs), MemberIDs: integers(g.MemberIDs), Delete: g.Delete}
	}
	return out
}

type mutationDTO struct {
	AccessVersion int       `json:"access_version"`
	Change        changeDTO `json:"change"`
	GraphRevision decimal   `json:"graph_revision"`
	ActorEpoch    decimal   `json:"actor_epoch"`
	Fingerprint   string    `json:"fingerprint"`
	Password      *string   `json:"password"`
}

func badAccessBody(c *gin.Context) error {
	c.JSON(http.StatusBadRequest, gin.H{"code": "invalid", "message": "Некорректный запрос управления доступом"})
	return nil
}
func readAccessJSON(c *gin.Context, dest any) error {
	// Strict decoding rejects mixed authority fields, unknown organization fields and preview secrets.
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4*1024*1024))
	dec.DisallowUnknownFields()
	if e := dec.Decode(dest); e != nil {
		return e
	}
	var extra any
	if e := dec.Decode(&extra); e != io.EOF {
		return fmt.Errorf("one JSON document required")
	}
	return nil
}
func (s *Server) requireModern() error {
	if !s.Access.Enabled() || !s.Access.RBACEnforced() {
		return domain.NewError(domain.Unavailable, "Изменение доступа недоступно до включения RBAC")
	}
	return nil
}
func (s *Server) authPreview(c *gin.Context) error {
	if e := s.requireModern(); e != nil {
		return e
	}
	var in struct {
		AccessVersion int       `json:"access_version"`
		Change        changeDTO `json:"change"`
	}
	if e := readAccessJSON(c, &in); e != nil || in.AccessVersion != 1 || in.Change.User != nil && in.Change.User.Organization.Null {
		return badAccessBody(c)
	}
	q, e := accessQuery(c)
	if e != nil {
		return e
	}
	v, e := s.Access.Preview(c.Request.Context(), in.Change.value(), q)
	if e != nil {
		return e
	}
	type snapshotDTO struct {
		accessFields
		Active bool               `json:"active"`
		Scope  domain.AccessScope `json:"scope"`
	}
	type impact struct {
		User                  accountDTO          `json:"user"`
		Before                snapshotDTO         `json:"before"`
		After                 snapshotDTO         `json:"after"`
		RetainedByOtherSource []domain.Permission `json:"retained_by_other_source"`
	}
	out := struct {
		GraphRevision decimal  `json:"graph_revision"`
		ActorEpoch    decimal  `json:"actor_epoch"`
		Fingerprint   string   `json:"fingerprint"`
		AssignedUsers int      `json:"assigned_users"`
		ChangedUsers  int      `json:"changed_users"`
		DisabledUsers int      `json:"disabled_users"`
		SelfAffected  bool     `json:"self_affected"`
		Items         []impact `json:"items"`
		NextCursor    *string  `json:"next_cursor"`
	}{decimal(v.GraphRevision), decimal(v.ActorEpoch), v.Fingerprint, v.AssignedUsers, v.ChangedUsers, v.DisabledUsers, v.SelfAffected, []impact{}, v.NextCursor}
	for _, i := range v.Items {
		before := domain.AccountAccessView{Account: i.User, Authorization: i.Before}
		after := domain.AccountAccessView{Account: i.User, Authorization: i.After}
		out.Items = append(out.Items, impact{accountViewDTO(before, true), snapshotDTO{viewFields(before, true), i.Before.Active, i.Before.LegacyScope}, snapshotDTO{viewFields(after, true), i.After.Active, i.After.LegacyScope}, append([]domain.Permission{}, i.RetainedByOtherSource...)})
	}
	return writeJSON(c, 200, out)
}
func (s *Server) applyMutation(c *gin.Context, in mutationDTO) error {
	if e := s.requireModern(); e != nil {
		return e
	}
	if in.AccessVersion != 1 || in.Change.User != nil && in.Change.User.Organization.Null {
		return badAccessBody(c)
	}
	change := in.Change.value()
	if strings.Contains(c.FullPath(), "/roles") {
		if change.Role == nil || change.User != nil || change.Group != nil {
			return badAccessBody(c)
		}
		if e := bindChangePath(c, "role_id", change.Role.ID, change.Role.Delete); e != nil {
			return e
		}
	}
	if strings.Contains(c.FullPath(), "/groups") {
		if change.Group == nil || change.User != nil || change.Role != nil {
			return badAccessBody(c)
		}
		if e := bindChangePath(c, "group_id", change.Group.ID, change.Group.Delete); e != nil {
			return e
		}
	}
	if strings.Contains(c.FullPath(), "/users") {
		if change.User == nil || change.Role != nil || change.Group != nil {
			return badAccessBody(c)
		}
		if e := bindChangePath(c, "user_id", change.User.ID, false); e != nil {
			return e
		}
	}
	v, e := s.Access.Apply(c.Request.Context(), domain.AccessApply{Change: change, GraphRevision: int64(in.GraphRevision), ActorEpoch: int64(in.ActorEpoch), Fingerprint: in.Fingerprint}, in.Password)
	if e != nil {
		return e
	}
	out := struct {
		User          *accountDTO `json:"user"`
		Role          *roleDTO    `json:"role"`
		Group         *groupDTO   `json:"group"`
		GraphRevision decimal     `json:"graph_revision"`
		AffectedUsers int         `json:"affected_users"`
	}{GraphRevision: decimal(v.GraphRevision), AffectedUsers: v.AffectedUsers}
	if v.UserView != nil {
		a := accountViewDTO(*v.UserView, true)
		out.User = &a
	}
	if v.Role != nil {
		r := accessRoleDTO(*v.Role)
		out.Role = &r
	}
	if v.Group != nil {
		g := accessGroupDTO(*v.Group)
		out.Group = &g
	}
	status := 200
	if c.Request.Method == "POST" {
		status = 201
	}
	return writeJSON(c, status, out)
}
func bindChangePath(c *gin.Context, key string, target *int64, deleted bool) error {
	if c.Request.Method == "POST" {
		if target != nil || deleted {
			return validation("Новый объект не должен иметь ID")
		}
		return nil
	}
	n, e := id(c, key)
	if e != nil {
		return e
	}
	if target == nil || *target != n || deleted != (c.Request.Method == "DELETE") {
		return validation("Изменение не соответствует маршруту")
	}
	return nil
}
func (s *Server) authMutation(c *gin.Context) error {
	var in mutationDTO
	if e := readAccessJSON(c, &in); e != nil {
		return badAccessBody(c)
	}
	return s.applyMutation(c, in)
}

func (o *organizationDTO) UnmarshalJSON(raw []byte) error {
	type plain organizationDTO
	var v plain
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&v); e != nil {
		return e
	}
	switch v.Kind {
	case "none", "organization":
		if v.NGDUID != nil || v.DepartmentID != nil {
			return fmt.Errorf("invalid organization shape")
		}
	case "ngdu":
		if v.NGDUID == nil || v.DepartmentID != nil {
			return fmt.Errorf("invalid ngdu shape")
		}
	case "department":
		if v.NGDUID == nil || v.DepartmentID == nil {
			return fmt.Errorf("invalid department shape")
		}
	default:
		return fmt.Errorf("unknown organization kind")
	}
	*o = organizationDTO(v)
	return nil
}
func readAccountWrite(c *gin.Context) (domain.AccountInput, *mutationDTO, error) {
	var raw map[string]json.RawMessage
	if e := readJSON(c, &raw); e != nil {
		return domain.AccountInput{}, nil, e
	}
	_, modern := raw["access_version"]
	if !modern {
		for _, key := range []string{"change", "role_ids", "group_ids", "organization", "organization_scope", "graph_revision", "actor_epoch", "fingerprint"} {
			if _, ok := raw[key]; ok {
				return domain.AccountInput{}, nil, fmt.Errorf("mixed access fields")
			}
		}
	}
	b, e := json.Marshal(raw)
	if e != nil {
		return domain.AccountInput{}, nil, e
	}
	if modern {
		var in mutationDTO
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if e = dec.Decode(&in); e != nil {
			return domain.AccountInput{}, nil, e
		}
		return domain.AccountInput{}, &in, nil
	}
	var in domain.AccountInput
	e = json.Unmarshal(b, &in)
	return in, nil, e
}
