# Command Execution

> [!CAUTION]
>
> The command runner and hooks are **disabled by default** and are **not
> considered safe**. Enabling them is equivalent to giving the configured users
> shell access to the host, with the privileges of the server process. To
> enable the runner from the default-off state, two opt-ins are required:
> `--disableExec=false` enables the code path, and `--enableUnsafeExec`
> acknowledges the risk. An instance whose stored settings already enable the
> runner still requires `--enableUnsafeExec` on every start; without it, the
> server refuses to start.
>
> The interactive allowlist is a convenience, not a security boundary. Many
> permitted programs can execute other programs (`find`, `git`, `env`, `sed`,
> interpreters, and so on). See
> [#5199](https://github.com/filebrowser/filebrowser/issues/5199).

## Command runner

The command runner lets an administrator allow a per-user list of commands in the
web UI. Commands are parsed into an argument vector and executed directly.

The configured `shell` setting is **not** applied. Wrapping the input in a shell
would let `ls; id` pass an allowlist that only contains `ls`, because the
allowlist authorizes the first token while the shell interprets the rest. If a
command needs a shell, invoke it explicitly and put the untrusted value in
double quotes:

```bash
sh -c 'cat -- "$FILE"'
```

Commands still run with the server's UID and are not confined to the user's
scope. A permitted program can read the database, other scopes, and anything else
the process can reach. Run File Browser unprivileged, in a container, with only
the served directory mounted. The interactive shell is disabled unless
`--disableExec=false --enableUnsafeExec` is passed.

## Hook runner

Hooks run administrator-defined commands before or after copy, rename, upload,
delete, and save events. As with the command runner, no shell is inserted
implicitly; write the shell invocation yourself when you need one.

Hook values are provided as **environment variables**, never interpolated into
the command string:

* `FILE` with the full absolute path to the changed file.
* `SCOPE` with the path to the user's scope.
* `TRIGGER` with the name of the event.
* `USERNAME` with the user's username.
* `DESTINATION` with the absolute path to the destination (copy and rename only).

A shell expands these variables from the environment as data. This matters for
filenames: a file named `; id #` stays an argument instead of becoming shell
syntax. A direct-exec hook that wants the value must invoke a shell, for example
`sh -c 'printf "%s\n" "$FILE"'`; a bare `echo $FILE` prints the literal text
because no shell is involved.

Manage hooks from the CLI:

```bash
filebrowser cmds add before_copy 'sh -c '\''printf "%s\n" "$FILE"'\'''
filebrowser cmds rm before_copy 0
filebrowser cmds ls
```

Or from **Settings** → **Global Settings** in the web UI.

> [!NOTE]
>
> If a proxy fronts File Browser, enable WebSocket support for the proxy.

> [!NOTE]
>
> Docker images ship a limited set of binaries. Build a custom image from the
> published one if a hook needs additional tools.
