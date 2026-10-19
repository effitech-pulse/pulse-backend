package domain

// WellFilter описывает выборку внутри уже разрешённой пользователю области.
type WellFilter struct {
	Archive    string
	Fund       string
	NGDU       *int64
	Department *int64
	Search     string
	Sort       string
}
