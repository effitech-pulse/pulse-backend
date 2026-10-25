package config

import (
	"os"
	"strings"
	"testing"
)

func TestNotificationsUIEnabledDefaultAndInvalid(t *testing.T) {
	t.Setenv("APP_SCOPE", "research")
	t.Setenv("FILE_CHECK_MODE", "off")
	t.Setenv("FILE_CHECK_ALLOW_REAL", "false")
	t.Setenv("COHORT_JOB_MODE", "off")
	t.Setenv("COHORT_JOB_ALLOW_REAL", "false")
	t.Setenv("COLLECTION_JOB_MODE", "off")
	// Load читает .env; пустой каталог доказывает именно code default.
	t.Chdir(t.TempDir())
	t.Setenv("NOTIFICATIONS_UI_ENABLED", "")
	if err := os.Unsetenv("NOTIFICATIONS_UI_ENABLED"); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || c.NotificationsUIEnabled {
		t.Fatalf("default must be off: enabled=%v, err=%v", c.NotificationsUIEnabled, err)
	}
	for _, tc := range []struct {
		value   string
		enabled bool
		valid   bool
	}{
		{"false", false, true}, {"true", true, true}, {"1", true, true}, {"0", false, true}, {"", false, false}, {"invalid", false, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("NOTIFICATIONS_UI_ENABLED", tc.value)
			c, err := Load()
			if (err == nil) != tc.valid {
				t.Fatalf("Load() error=%v, valid=%v", err, tc.valid)
			}
			if tc.valid && c.NotificationsUIEnabled != tc.enabled {
				t.Fatalf("enabled=%v, want %v", c.NotificationsUIEnabled, tc.enabled)
			}
			if !tc.valid && !strings.Contains(err.Error(), "NOTIFICATIONS_UI_ENABLED") {
				t.Fatalf("missing configuration field in error: %v", err)
			}
		})
	}
}
