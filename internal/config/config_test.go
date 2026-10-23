package config

import (
	"strings"
	"testing"
)

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

func TestCohortJobsFailClosedConfiguration(t *testing.T) {
	t.Setenv("APP_SCOPE", "research")
	t.Setenv("FILE_CHECK_MODE", "off")
	for _, tc := range []struct{ key, value string }{
		{"COHORT_JOB_MODE", "real"}, {"COHORT_JOB_ALLOW_REAL", "true"}, {"COHORT_JOB_ALLOW_REAL", "bad"}, {"COHORT_JOB_MODE", "synthetic"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv("COHORT_JOB_MODE", "off")
			t.Setenv("COHORT_JOB_ALLOW_REAL", "false")
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("unsafe cohort job configuration accepted")
			}
		})
	}
}

func TestCohortJobsRequireIsolatedRuntime(t *testing.T) {
	defaults := map[string]string{"APP_SCOPE": "cohort", "COHORT_VERSION": "test-v1", "AUTH_MODE": "local", "FILE_CHECK_MODE": "off", "COHORT_JOB_MODE": "synthetic", "COHORT_JOB_ALLOW_REAL": "false", "COHORT_JOB_MANIFEST": "/synthetic/manifest.json", "COHORT_JOB_MANIFEST_SHA256": "sha256:" + strings.Repeat("a", 64), "COHORT_JOB_RUN_ID": "q2-test", "COHORT_JOB_TEST_SCHEMA": "q2_test", "COHORT_JOB_ML_TARGET": "127.0.0.1:15052", "ML_SERVICE_TARGET": "127.0.0.1:15051", "COHORT_JOB_AMQP_URL": "amqp://localhost/pulse-q2-synthetic", "COHORT_JOB_PRODUCER_AMQP_URL": "amqp://localhost/pulse-q2-synthetic"}
	for key, value := range defaults {
		t.Setenv(key, value)
	}
	c, err := Load()
	if err != nil || c.CohortJobMode != "synthetic" {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, value string }{
		{"COHORT_JOB_ML_TARGET", "127.0.0.1:15051"}, {"COHORT_JOB_TEST_SCHEMA", "public"}, {"COHORT_JOB_TEST_SCHEMA", "pg_unsafe"}, {"COHORT_JOB_MANIFEST_SHA256", "unknown"}, {"COHORT_JOB_AMQP_URL", "amqp://localhost/pulse-file-check"}, {"COHORT_JOB_PRODUCER_AMQP_URL", ""}, {"COHORT_JOB_RUN_ID", ""}, {"AUTH_MODE", "legacy"}, {"APP_SCOPE", "research"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("unisolated runtime accepted")
			}
		})
	}
}

func TestCollectionConfigDefaultOffAndSyntheticGuard(t *testing.T) {
	t.Setenv("APP_SCOPE", "research")
	t.Setenv("AUTH_MODE", "local")
	t.Setenv("COLLECTION_JOB_MODE", "real")
	if _, e := Load(); e == nil {
		t.Fatal("real collection accepted")
	}
	t.Setenv("COLLECTION_JOB_MODE", "synthetic")
	if _, e := Load(); e == nil {
		t.Fatal("synthetic missing guard accepted")
	}
	t.Setenv("COLLECTION_JOB_MODE", "off")
	if _, e := Load(); e != nil {
		t.Fatal(e)
	}
}

func TestCollectionSyntheticConfigurationRequiresIsolation(t *testing.T) {
	defaults := map[string]string{"COLLECTION_JOB_MODE": "synthetic", "COLLECTION_JOB_MANIFEST": "/synthetic/manifest.json", "COLLECTION_JOB_MANIFEST_SHA256": strings.Repeat("a", 64), "COLLECTION_JOB_RUN_ID": "test-run", "COLLECTION_JOB_TEST_SCHEMA": "q2c2_test", "COLLECTION_JOB_AMQP_URL": "amqp://localhost/pulse-q2c2-synthetic", "COLLECTION_JOB_PRODUCER_AMQP_URL": "amqp://localhost/pulse-q2c2-synthetic", "APP_SCOPE": "research", "AUTH_MODE": "local", "DB_POOL_SIZE": "8"}
	for k, v := range defaults {
		t.Setenv(k, v)
	}
	if c, e := Load(); e != nil || c.CollectionJobMode != "synthetic" {
		t.Fatal(e)
	}
	for _, tc := range []struct{ key, value string }{{"COLLECTION_JOB_MODE", "real"}, {"COLLECTION_JOB_TEST_SCHEMA", "public"}, {"COLLECTION_JOB_TEST_SCHEMA", "pg_catalog"}, {"COLLECTION_JOB_MANIFEST_SHA256", "sha256:" + strings.Repeat("a", 64)}, {"COLLECTION_JOB_AMQP_URL", "amqp://localhost/"}, {"COLLECTION_JOB_PRODUCER_AMQP_URL", ""}, {"APP_SCOPE", "cohort"}, {"AUTH_MODE", "legacy"}, {"DB_POOL_SIZE", "9"}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, e := Load(); e == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}
