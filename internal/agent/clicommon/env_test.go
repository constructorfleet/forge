package clicommon

import (
	"os"
	"strings"
	"testing"
)

func TestSanitizedEnv_OnlyForwardsAllowedAndSetVars(t *testing.T) {
	t.Setenv("FORGE_CLICOMMON_TEST_ALLOWED", "yes")
	t.Setenv("FORGE_CLICOMMON_TEST_SECRET", "shh")
	os.Unsetenv("FORGE_CLICOMMON_TEST_UNSET")

	env := SanitizedEnv([]string{"FORGE_CLICOMMON_TEST_ALLOWED", "FORGE_CLICOMMON_TEST_UNSET"}, nil, nil)

	if !containsPrefix(env, "FORGE_CLICOMMON_TEST_ALLOWED=yes") {
		t.Errorf("SanitizedEnv() = %v, want to include the allowed var", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "FORGE_CLICOMMON_TEST_SECRET") {
			t.Errorf("SanitizedEnv() = %v, must not forward unlisted secret var", env)
		}
	}
}

func TestSanitizedEnv_MergesAllowedAuthAndExtraWithoutDuplicates(t *testing.T) {
	t.Setenv("FORGE_CLICOMMON_TEST_A", "a")
	t.Setenv("FORGE_CLICOMMON_TEST_B", "b")

	env := SanitizedEnv(
		[]string{"FORGE_CLICOMMON_TEST_A"},
		[]string{"FORGE_CLICOMMON_TEST_A", "FORGE_CLICOMMON_TEST_B"},
		[]string{"FORGE_CLICOMMON_TEST_B"},
	)

	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "FORGE_CLICOMMON_TEST_A=") || strings.HasPrefix(e, "FORGE_CLICOMMON_TEST_B=") {
			count++
		}
	}
	if count != 2 {
		t.Errorf("SanitizedEnv() = %v, want each var forwarded exactly once", env)
	}
}

func containsPrefix(env []string, prefix string) bool {
	for _, e := range env {
		if e == prefix {
			return true
		}
	}
	return false
}
