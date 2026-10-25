package config

import "testing"

func TestDailyFlowConfig(t *testing.T) {
	for _, c := range []Config{{DailyFlowMode: "real"}, {DailyFlowMode: "synthetic", AuthMode: "legacy", AppScope: "cohort", CohortVersion: "presentation-v1"}, {DailyFlowMode: "synthetic", AuthMode: "local", AppScope: "research", CohortVersion: "presentation-v1"}, {DailyFlowMode: "synthetic", AuthMode: "local", AppScope: "cohort", CohortVersion: "other"}} {
		if e := ValidateDailyFlow(c); e == nil {
			t.Fatal(c)
		}
	}
	for _, c := range []Config{{DailyFlowMode: "off"}, {DailyFlowMode: "synthetic", AuthMode: "local", AppScope: "cohort", CohortVersion: "presentation-v1"}} {
		if e := ValidateDailyFlow(c); e != nil {
			t.Fatal(e)
		}
	}
}
