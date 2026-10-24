package domain

// AccessRole — изменяемое определение роли; BuiltinKey — только совместимая проекция.
type AccessRole struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	BuiltinKey     *string      `json:"builtin_key"`
	Revision       int64        `json:"revision"`
	PermissionKeys []Permission `json:"permission_keys"`
}

type AccessGroup struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Revision    int64   `json:"revision"`
	RoleIDs     []int64 `json:"role_ids"`
}

type UserAccessChange struct {
	Profile       Field[ProfileInput]      `json:"profile"`
	ID            *int64                   `json:"id"`
	Login         string                   `json:"login"`
	Name          string                   `json:"name"`
	Active        bool                     `json:"active"`
	Revision      int64                    `json:"revision"`
	RoleIDs       []int64                  `json:"role_ids"`
	GroupIDs      []int64                  `json:"group_ids"`
	Organization  Field[OrganizationScope] `json:"organization"`
	ResetPassword bool                     `json:"reset_password"`
}

type RoleAccessChange struct {
	ID             *int64       `json:"id"`
	Revision       int64        `json:"revision"`
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	PermissionKeys []Permission `json:"permission_keys"`
	Delete         bool         `json:"delete"`
}

type GroupAccessChange struct {
	ID          *int64  `json:"id"`
	Revision    int64   `json:"revision"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	RoleIDs     []int64 `json:"role_ids"`
	MemberIDs   []int64 `json:"member_ids"`
	Delete      bool    `json:"delete"`
}

// AccessChange содержит ровно один вариант; пароль передаётся сервису отдельно.
type AccessChange struct {
	User  *UserAccessChange  `json:"user"`
	Role  *RoleAccessChange  `json:"role"`
	Group *GroupAccessChange `json:"group"`
}

type AccessImpact struct {
	User                  Account               `json:"user"`
	Before                AuthorizationSnapshot `json:"before"`
	After                 AuthorizationSnapshot `json:"after"`
	RetainedByOtherSource []Permission          `json:"retained_by_other_source"`
}

type AccessPreview struct {
	ProfileChange *ProfileChangeSummary `json:"profile_change"`
	SessionEffect string                `json:"session_effect"`
	GraphRevision int64                 `json:"graph_revision"`
	ActorEpoch    int64                 `json:"actor_epoch"`
	Fingerprint   string                `json:"fingerprint"`
	AssignedUsers int                   `json:"assigned_users"`
	ChangedUsers  int                   `json:"changed_users"`
	DisabledUsers int                   `json:"disabled_users"`
	SelfAffected  bool                  `json:"self_affected"`
	Items         []AccessImpact        `json:"items"`
	NextCursor    *string               `json:"next_cursor"`
}

type AccessApply struct {
	Change        AccessChange `json:"change"`
	GraphRevision int64        `json:"graph_revision"`
	ActorEpoch    int64        `json:"actor_epoch"`
	Fingerprint   string       `json:"fingerprint"`
}

type AccessApplyResult struct {
	SessionEffect string             `json:"session_effect"`
	UserView      *AccountAccessView `json:"user_view"`
	User          *Account           `json:"user"`
	Role          *AccessRole        `json:"role"`
	Group         *AccessGroup       `json:"group"`
	GraphRevision int64              `json:"graph_revision"`
	AffectedUsers int                `json:"affected_users"`
}

type AccessPageQuery struct {
	Search        string
	Limit         int
	Cursor        string
	GraphRevision *int64
}

type AccountPage struct {
	Items         []Account `json:"items"`
	NextCursor    *string   `json:"next_cursor"`
	TotalVisible  int       `json:"total_visible"`
	GraphRevision int64     `json:"graph_revision"`
}

type RolePage struct {
	Items         []AccessRole `json:"items"`
	NextCursor    *string      `json:"next_cursor"`
	TotalVisible  int          `json:"total_visible"`
	GraphRevision int64        `json:"graph_revision"`
}

type GroupPage struct {
	Items         []AccessGroup `json:"items"`
	NextCursor    *string       `json:"next_cursor"`
	TotalVisible  int           `json:"total_visible"`
	GraphRevision int64         `json:"graph_revision"`
}

// OrganizationPair отражает существующую пару wells, не корпоративную иерархию.
type OrganizationPair struct {
	NGDUID       int64 `json:"ngdu_id"`
	DepartmentID int64 `json:"department_id"`
}

type OrganizationOptions struct {
	NGDUs       []Reference        `json:"ngdus"`
	Departments []Reference        `json:"departments"`
	Pairs       []OrganizationPair `json:"pairs"`
	ActorScope  *OrganizationScope `json:"actor_scope"`
}
