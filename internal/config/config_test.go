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

func TestFileJobsSyntheticGuardDefaultOff(t *testing.T) {
	t.Setenv("APP_SCOPE", "research")
	t.Setenv("FILE_CHECK_MODE", "")
	t.Setenv("FILE_CHECK_ALLOW_REAL", "false")
	if _, err := Load(); err == nil {
		t.Fatal("empty explicit mode accepted")
	}
	t.Setenv("FILE_CHECK_MODE", "off")
	c, err := Load()
	if err != nil || c.FileCheckMode != "off" {
		t.Fatal(c, err)
	}
	t.Setenv("FILE_CHECK_ALLOW_REAL", "true")
	if _, err = Load(); err == nil {
		t.Fatal("real file capability enabled")
	}
	t.Setenv("FILE_CHECK_ALLOW_REAL", "false")
	t.Setenv("FILE_CHECK_MODE", "synthetic")
	t.Setenv("AUTH_MODE", "legacy")
	if _, err = Load(); err == nil {
		t.Fatal("synthetic without local session accepted")
	}
	t.Setenv("AUTH_MODE", "local")
	if _, err = Load(); err == nil {
		t.Fatal("synthetic without manifest accepted")
	}
}
