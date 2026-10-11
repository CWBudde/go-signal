//go:build cgo || libsignal_go

package accountimport_test

import (
	"os"
	"testing"
)

func TestJavaAccountImportSessionInspection(t *testing.T) {
	t.Parallel()

	corpus := loadJavaCorpus(t)
	for _, account := range corpus.Accounts {
		t.Run(account.ID, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			restoreJavaAccount(t, dir, account.ID)

			reopened := openJavaAccount(t, dir, account.ID)
			defer reopened.close(t)

			verifyRestored(t, reopened, account, corpus)
		})
	}
}

func TestJavaAccountImportProtocolContinuation(t *testing.T) {
	t.Parallel()

	for _, scenario := range loadJavaCorpus(t).Scenarios {
		t.Run(scenario.ID, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			compatibilityStep(t, dir, scenario.ID, "init")

			for range 3 {
				compatibilityStep(t, dir, scenario.ID, "advance")
			}
		})
	}
}

func TestJavaAccountImportConsumption(t *testing.T) {
	t.Parallel()

	for _, scenario := range loadJavaCorpus(t).Scenarios {
		if scenario.ConsumedECID == nil {
			continue
		}

		t.Run(scenario.ID, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			compatibilityStep(t, dir, scenario.ID, "init")
			compatibilityStep(t, dir, scenario.ID, "advance")

			reopened := openJavaAccount(t, dir, scenario.SourceAccountID)
			defer reopened.close(t)

			checkConsumption(t, reopened, scenario)
		})
	}
}

func TestJavaAccountImportFailureState(t *testing.T) {
	t.Parallel()

	for _, scenario := range loadJavaCorpus(t).Scenarios {
		t.Run(scenario.ID, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			compatibilityStep(t, dir, scenario.ID, "init")
			compatibilityStep(t, dir, scenario.ID, "advance")
			compatibilityStep(t, dir, scenario.ID, "advance") // replay/tamper assertions run after reopen
		})
	}
}

func TestJavaAccountImportBackendStep(t *testing.T) {
	t.Parallel()

	dir := os.Getenv("GOSIGNAL_IMPORT_COMPAT_DIR")
	if dir == "" {
		t.Skip("run scripts/test-account-import-compatibility.sh")
	}

	scenario := os.Getenv("GOSIGNAL_IMPORT_COMPAT_SCENARIO")

	action := os.Getenv("GOSIGNAL_IMPORT_COMPAT_ACTION")
	if scenario == "" || action == "" {
		t.Fatal("missing compatibility subprocess parameters")
	}

	compatibilityStep(t, dir, scenario, action)
}
