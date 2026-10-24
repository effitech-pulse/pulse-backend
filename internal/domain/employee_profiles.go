package domain

// Workplace содержит только информационные сведения, без полномочий.
type Workplace struct {
	Organization *string `json:"organization"`
	Division     *string `json:"division"`
	Department   *string `json:"department"`
}
type ManagedProfile struct {
	Revision  int64     `json:"revision,string"`
	JobTitle  *string   `json:"job_title"`
	Workplace Workplace `json:"workplace"`
	ManagerID *int64    `json:"manager_id,string"`
}
type WorkplaceInput struct {
	Organization Field[string] `json:"organization"`
	Division     Field[string] `json:"division"`
	Department   Field[string] `json:"department"`
}
type ProfileInput struct {
	Revision  Field[int64]          `json:"revision"`
	JobTitle  Field[string]         `json:"job_title"`
	Workplace Field[WorkplaceInput] `json:"workplace"`
	ManagerID Field[int64]          `json:"manager_id"`
}
type ColleagueRef struct {
	ID       int64   `json:"id,string"`
	Name     string  `json:"name"`
	JobTitle *string `json:"job_title"`
	Active   bool    `json:"active"`
}
type EmployeeProfile struct {
	ID        int64         `json:"id,string"`
	Name      string        `json:"name"`
	Active    bool          `json:"active"`
	JobTitle  *string       `json:"job_title"`
	Workplace Workplace     `json:"workplace"`
	Manager   *ColleagueRef `json:"manager"`
}
type PeopleQuery struct {
	Search          string
	IncludeInactive bool
	Limit           int
	Cursor          string
}
type PeoplePage struct {
	Items      []EmployeeProfile `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}
type NamedProfile struct {
	Name    string         `json:"name"`
	Profile ManagedProfile `json:"profile"`
}
type ProfileChangeSummary struct {
	Before *NamedProfile `json:"before"`
	After  NamedProfile  `json:"after"`
}
