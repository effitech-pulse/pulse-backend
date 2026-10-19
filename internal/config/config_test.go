package config

import "testing"

func TestUserProfileDefaultsFailClosed(t *testing.T) {
	t.Setenv("APP_SCOPE", "cohort")
	t.Setenv("COHORT_VERSION", "")
	if _, err := Load(); err == nil {
		t.Fatal("missing cohort accepted")
	}
	t.Setenv("COHORT_VERSION", "presentation-v1")
	c, err := Load()
	if err != nil || c.AppScope != "cohort" || c.CohortVersion != "presentation-v1" {
		t.Fatal(c.AppScope, err)
	}
	t.Setenv("APP_SCOPE", "research")
	t.Setenv("COHORT_VERSION", "")
	if _, err = Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_SCOPE", "all")
	if _, err = Load(); err == nil {
		t.Fatal("unknown profile accepted")
	}
}
