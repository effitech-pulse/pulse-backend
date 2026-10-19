package domain

import "time"

type PresentationCheck struct {
	ID           string    `json:"id"`
	Verdict      string    `json:"verdict"`
	LatestRisk   float64   `json:"latest_risk"`
	CreatedAt    time.Time `json:"created_at"`
	ModelVersion *string   `json:"model_version"`
}
type PresentationWell struct {
	Well
	LatestCheck *PresentationCheck `json:"latest_check"`
}
type PresentationSummary struct {
	Wells        []PresentationWell `json:"wells"`
	Count        int                `json:"count"`
	Checked      int                `json:"checked"`
	Watch        int                `json:"watch"`
	Alert        int                `json:"alert"`
	Insufficient int                `json:"insufficient"`
}
