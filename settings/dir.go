package settings

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/users"
)

var (
	invalidFilenameChars = regexp.MustCompile(`[^0-9A-Za-z@_\-.]`)

	dashes = regexp.MustCompile(`[\-]+`)
)

// ErrRootScope is returned when a provisioning flow cannot derive a per-user
// scope and would otherwise persist the whole server root as a user's scope.
var ErrRootScope = errors.New("provisioning would grant the server root scope")

// isRootScope reports whether scope is the server root after normalization by
// MakeUserDir (which uses path.Join("/", scope)).
func isRootScope(scope string) bool {
	s := strings.TrimSpace(scope)
	return s == "" || s == "." || s == "/"
}

// MakeUserDir makes the user directory according to settings.
func (s *Settings) MakeUserDir(username, userScope, serverRoot string) (string, error) {
	userScope = strings.TrimSpace(userScope)
	if userScope == "" && s.CreateUserDir {
		username = cleanUsername(username)
		if username == "" || username == "-" || username == "." {
			log.Printf("create user: invalid user for home dir creation: [%s]", username)
			return "", errors.New("invalid user for home dir creation")
		}
		userScope = path.Join(s.UserHomeBasePath, username)
	}

	userScope = path.Join("/", userScope)

	fs := afero.NewBasePathFs(afero.NewOsFs(), serverRoot)
	if err := fs.MkdirAll(userScope, os.ModePerm); err != nil {
		return "", fmt.Errorf("failed to create user home dir: [%s]: %w", userScope, err)
	}
	return userScope, nil
}

// CreateUserHome derives and creates the home directory for a user that is
// being provisioned (via signup, proxy auth or hook auth) and sets user.Scope
// to the resulting path. When CreateUserDir is enabled and the caller did not
// supply an explicit scope, the scope is cleared so that MakeUserDir derives a
// per-user home from the username instead of falling back to the default scope
// (which normalizes to the server root, leaving every provisioned user sharing
// it).
//
// It reports whether the scope was derived from the username. A derived scope
// must be persisted with users.Storage.SaveProvisioned, which rejects a scope
// already owned by another user so that distinct usernames cannot silently
// share one home directory.
//
// Provisioning is refused with ErrRootScope when no per-user scope can be
// derived, no explicit scope was configured, and the scope would resolve to
// "/". An administrator who wants several provisioned users to share a tree
// must configure an explicit, non-root scope; "leave createUserDir off" must
// never hand out the server root.
//
// An explicitly provided scope — today only hook auth passes one, from the
// administrator's own script — is trusted as admin intent and is never refused
// here, even when it is the root: administrators can already grant a root scope
// through the UI or CLI. Signup and proxy auto-provisioning always pass
// explicitScope=false, so they can never provision the server root.
func (s *Settings) CreateUserHome(user *users.User, serverRoot string, explicitScope bool) (derived bool, err error) {
	derived = s.CreateUserDir && !explicitScope
	if !explicitScope && !derived && isRootScope(user.Scope) {
		return false, ErrRootScope
	}

	scope := user.Scope
	if derived {
		scope = ""
	}

	userHome, err := s.MakeUserDir(user.Username, scope, serverRoot)
	if err != nil {
		return false, err
	}
	if !explicitScope && isRootScope(userHome) {
		return false, ErrRootScope
	}
	user.Scope = userHome

	return derived, nil
}

func cleanUsername(s string) string {
	// Remove any trailing space to avoid ending on -
	s = strings.Trim(s, " ")
	s = strings.ReplaceAll(s, "..", "")

	// Replace all characters which not in the list `0-9A-Za-z@_\-.` with a dash
	s = invalidFilenameChars.ReplaceAllString(s, "-")

	// Remove any multiple dashes caused by replacements above
	s = dashes.ReplaceAllString(s, "-")
	return s
}
