package httpapi

type dailyFlowDisabledDTO struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}
