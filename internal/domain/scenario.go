package domain

type PresentationScenario struct {
	ScenarioID     string  `json:"scenario_id"`
	Origin         string  `json:"origin"`
	Seed           int64   `json:"seed"`
	Count          int     `json:"count"`
	Affected       int     `json:"affected"`
	RampDays       float64 `json:"ramp_days"`
	ProfileCount   int     `json:"profile_count"`
	ProfilesSHA256 string  `json:"profiles_sha256"`
	Notice         string  `json:"notice"`
}
