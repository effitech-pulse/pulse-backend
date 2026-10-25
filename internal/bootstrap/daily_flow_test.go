package bootstrap

import (
	"context"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"testing"
	"time"
)

func TestDailyFlowOffNeedsNoProvider(t *testing.T) {
	s, e := buildDailyFlow(context.Background(), config.Config{DailyFlowMode: "off"}, nil, nil, time.Time{})
	if e != nil || s == nil {
		t.Fatal(e)
	}
}
func TestDailyFlowSyntheticNeedsExactCohort(t *testing.T) {
	if _, e := buildDailyFlow(context.Background(), config.Config{DailyFlowMode: "synthetic", AuthMode: "local", AppScope: "cohort", CohortVersion: "presentation-v1"}, nil, nil, time.Now()); e == nil {
		t.Fatal("no cohort accepted")
	}
}
