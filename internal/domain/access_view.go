package domain

type AccessRoleSummary struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Revision   int64   `json:"revision"`
	BuiltinKey *string `json:"builtin_key"`
}
type AccessGroupMembership struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Revision int64   `json:"revision"`
	RoleIDs  []int64 `json:"role_ids"`
}
type AccessAssignments struct {
	DirectRoleIDs []int64                 `json:"direct_role_ids"`
	Groups        []AccessGroupMembership `json:"groups"`
	Roles         []AccessRoleSummary     `json:"roles"`
}

type AccountAccessView struct {
	Account       Account               `json:"account"`
	Authorization AuthorizationSnapshot `json:"authorization"`
}
