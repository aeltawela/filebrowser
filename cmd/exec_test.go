package cmd

import (
	"testing"

	"github.com/spf13/viper"

	"github.com/filebrowser/filebrowser/v2/settings"
)

// Regression for GHSA-3q2w-42mv-cph4 and GHSA-hc8f-m8g5-8362: enabling the
// command runner is equivalent to shell access, so it requires an explicit
// acknowledgement instead of a single disable flag.
func TestValidateUnsafeExecRequiresAcknowledgement(t *testing.T) {
	v := viper.New()

	if err := validateUnsafeExec(v, &settings.Server{EnableExec: true}); err == nil {
		t.Fatal("expected enabling the command runner without acknowledgement to fail")
	}

	v.Set("enableUnsafeExec", true)
	if err := validateUnsafeExec(v, &settings.Server{EnableExec: true}); err != nil {
		t.Fatalf("expected acknowledged command runner to be accepted, got %v", err)
	}

	if err := validateUnsafeExec(v, &settings.Server{EnableExec: false}); err != nil {
		t.Fatalf("disabled command runner must not require acknowledgement, got %v", err)
	}
}
