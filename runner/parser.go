package runner

import (
	"github.com/filebrowser/filebrowser/v2/settings"
)

// ParseCommand parses a command into an argv that is executed directly.
//
// A configured shell is deliberately not used, neither for interactive
// commands nor for hooks. Implicitly wrapping the configured command in a shell
// turns admin-authored `$FILE`/`$USERNAME` references into script text: the
// shell expands the value before invoking the real program, and any shell
// syntax in that value is interpreted one level later. Executing the parsed
// argv directly keeps those values as data.
//
// Administrators who need a shell should invoke it explicitly, for example
// `sh -c 'cat -- "$FILE"'`, and keep the interpolated value in double quotes.
//
// The settings parameter is retained so callers do not need to change, and to
// document that a configured shell is intentionally ignored.
func ParseCommand(_ *settings.Settings, raw string) (command []string, name string, err error) {
	name, args, err := SplitCommandAndArgs(raw)
	if err != nil {
		return
	}

	command = append(command, name)
	command = append(command, args...)

	return command, name, nil
}
