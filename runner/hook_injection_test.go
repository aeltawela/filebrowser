package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/users"
)

// A configured shell must not be used for interactive commands: the allowlist
// authorizes only the first token, so passing the raw input to a shell would let
// `ls; id` run `id` after `ls`. Direct execution makes the metacharacters part
// of an argument and the command name no longer matches the allowlist.
func TestParseCommandDoesNotUseShell(t *testing.T) {
	s := &settings.Settings{Shell: []string{"/bin/sh", "-c"}}

	command, name, err := ParseCommand(s, "ls; id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name == "ls" {
		t.Fatalf("VULNERABLE: shell syntax still resolves to the allowlisted name %q", name)
	}
	if command[0] == "/bin/sh" {
		t.Fatalf("VULNERABLE: interactive command still runs through the configured shell: %#v", command)
	}
}

// A plain command must keep its exact argv when no shell is involved.
func TestParseCommandKeepsArgv(t *testing.T) {
	command, name, err := ParseCommand(&settings.Settings{}, `ls -la "two words"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name != "ls" {
		t.Fatalf("expected name ls, got %q", name)
	}
	want := []string{"ls", "-la", "two words"}
	if len(command) != len(want) {
		t.Fatalf("expected %#v, got %#v", want, command)
	}
	for i := range want {
		if command[i] != want[i] {
			t.Fatalf("expected %#v, got %#v", want, command)
		}
	}
}

// A configured shell must not be applied implicitly to hooks either: it adds a
// second parsing layer where an expanded filename becomes script syntax.
func TestParseCommandDoesNotApplyShellToHooks(t *testing.T) {
	s := &settings.Settings{Shell: []string{"/bin/sh", "-c"}}

	command, _, err := ParseCommand(s, `sh -c "echo $FILE"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(command) != 3 || command[0] != "sh" || command[1] != "-c" || command[2] != "echo $FILE" {
		t.Fatalf("unexpected hook command: %#v", command)
	}
}

// Regression for GHSA-jvpw-637p-h3pw: a filename containing shell syntax must
// reach the hook as data through the environment, not as part of the command
// string. The hook still sees $FILE because the shell expands the environment
// variable, but the expanded value cannot introduce a command separator.
func TestHookFilenameIsNotInterpretedAsShellSyntax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "injected")

	// If the value were interpolated into the command string, the shell would
	// see `echo created /<scope>/; touch <marker> #` and create the marker.
	malicious := "; touch " + marker + " #"

	r := &Runner{
		Enabled: true,
		Settings: &settings.Settings{
			Shell: []string{"sh", "-c"},
			Commands: map[string][]string{
				"after_upload": {`sh -c "echo created $FILE"`},
			},
		},
	}
	u := &users.User{
		Username: "u",
		Scope:    dir,
		Fs:       afero.NewBasePathFs(afero.NewOsFs(), dir),
	}

	if err := r.RunHook(func() error { return nil }, "upload", malicious, "", u); err != nil {
		t.Fatalf("hook failed: %v", err)
	}

	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("VULNERABLE: hook executed a command injected through the filename")
	}
}
