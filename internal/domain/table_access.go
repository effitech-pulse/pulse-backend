package domain

// AccessFilterOption содержит только уже раскрытые ID и подписи разрешённых назначений.
type AccessFilterOption struct {
	Value int64  `json:"value,string"`
	Label string `json:"label"`
}
type AccessFilterOptionsPage struct {
	Items         []AccessFilterOption `json:"items"`
	NextCursor    *string              `json:"next_cursor"`
	GraphRevision int64                `json:"graph_revision,string"`
}

// AccessPreviewFilterOptions — полный набор уже раскрытых источников после изменения.
type AccessPreviewFilterOptions struct {
	SourceRoleIDs  []AccessFilterOption `json:"source_role_ids"`
	SourceGroupIDs []AccessFilterOption `json:"source_group_ids"`
}
