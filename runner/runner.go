package runner

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/users"
)

// Runner is a commands runner.
type Runner struct {
	Enabled bool
	*settings.Settings
}

// RunHook runs the hooks for the before and after event.
func (r *Runner) RunHook(fn func() error, evt, path, dst string, user *users.User) error {
	path = user.FullPath(path)
	dst = user.FullPath(dst)

	if r.Enabled {
		if val, ok := r.Commands["before_"+evt]; ok {
			for _, command := range val {
				err := r.exec(command, "before_"+evt, path, dst, user)
				if err != nil {
					return err
				}
			}
		}
	}

	err := fn()
	if err != nil {
		return err
	}

	if r.Enabled {
		if val, ok := r.Commands["after_"+evt]; ok {
			for _, command := range val {
				err := r.exec(command, "after_"+evt, path, dst, user)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (r *Runner) exec(raw, evt, path, dst string, user *users.User) error {
	blocking := true

	if strings.HasSuffix(raw, "&") {
		blocking = false
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "&"))
	}

	command, _, err := ParseCommand(r.Settings, raw)
	if err != nil {
		return err
	}

	// Hook values are exported through the environment below and are never
	// interpolated into the command. Expanding them into argv made a crafted
	// filename (for example `; id #`) part of a shell command string and turned
	// every hook that embedded $FILE into command injection. A shell still
	// expands $FILE from the environment when a hook uses shell syntax, but the
	// expanded value is data, not syntax.
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("FILE=%s", path))
	cmd.Env = append(cmd.Env, fmt.Sprintf("SCOPE=%s", user.Scope))
	cmd.Env = append(cmd.Env, fmt.Sprintf("TRIGGER=%s", evt))
	cmd.Env = append(cmd.Env, fmt.Sprintf("USERNAME=%s", user.Username))
	cmd.Env = append(cmd.Env, fmt.Sprintf("DESTINATION=%s", dst))

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if !blocking {
		log.Printf("[INFO] Nonblocking Command: \"%s\"", strings.Join(command, " "))
		defer func() {
			go func() {
				err := cmd.Wait()
				if err != nil {
					log.Printf("[INFO] Nonblocking Command \"%s\" failed: %s", strings.Join(command, " "), err)
				}
			}()
		}()
		return cmd.Start()
	}

	log.Printf("[INFO] Blocking Command: \"%s\"", strings.Join(command, " "))
	return cmd.Run()
}
