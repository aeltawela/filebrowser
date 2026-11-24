# Authentication

There are three possible authentication methods. Each one of them has its own capabilities and specification. Adding another authentication method is described in [Building File Browser](../CONTRIBUTING.md#authentication-provider).

## JSON Auth (default)

We call it JSON Authentication but it is just the default authentication method and the one that is provided by default if you don't make any changes. It is set by default, but if you've made changes before you can revert to using JSON auth:

```sh
filebrowser config set --auth.method=json
```

This method can also be extended with **reCAPTCHA** verification during login:

```sh
filebrowser config set --auth.method=json \
  --recaptcha.key site-key \
  --recaptcha.secret private-key
```

By default, we use [Google's reCAPTCHA](https://developers.google.com/recaptcha/docs/display) service. If you live in China, or want to use other provider, you can change the host with the following command:

```sh
filebrowser config set --recaptcha.host https://recaptcha.net
```

Where `https://recaptcha.net` is any provider you want.

## Proxy Header

If you have a reverse proxy you want to use to login your users, you do it via our `proxy` authentication method. To configure this method, your proxy must send an HTTP header containing the username of the logged in user:

```sh
filebrowser config set --auth.method=proxy --auth.header=X-My-Header \
  --auth.proxy.trustedProxies=127.0.0.1/32
```

Where `X-My-Header` is the HTTP header provided by your proxy with the username.

> [!IMPORTANT]
>
> The header is an identity assertion, and File Browser only trusts it when
> the request comes from a configured trusted source. **The server refuses to
> start** with `auth.method=proxy` unless at least one trust anchor is
> configured. `X-Forwarded-For` and similar headers are client-controlled and
> are never used as a trust anchor; the source address (`RemoteAddr`) is.

Trust anchors:

- `--auth.proxy.trustedProxies` — a comma-separated list of CIDRs whose source
  address may assert a username, for example `127.0.0.1/32,::1/128` when the
  proxy runs on the same host. Use the address File Browser actually sees: if
  the proxy connects from another container or host, list that network.
- `--auth.proxy.secret` — a shared secret the proxy must echo in the
  `X-Proxy-Secret` header. When both a CIDR list and a secret are configured,
  both must match.

Additional controls:

- `--auth.proxy.createUser` — allows a trusted proxy to provision previously
  unknown users. Off by default: an identity assertion must not silently
  create accounts. Provisioned users get a derived per-user scope
  (`createUserDir`) or an explicitly configured non-root scope; provisioning
  the server root is refused.
- `--auth.proxy.allowUntrusted` — explicit acknowledgement that the instance
  is only reachable from trusted clients (for example, bound to loopback).
  This is the only way to run proxy auth without a trust anchor, and it
  disables the protection entirely. Prefer a trusted CIDR.

Your proxy must still strip or overwrite the identity header (and
`X-Proxy-Secret`, if used) from client requests.

## Sessions and revocation

Sessions are JWTs carrying a session epoch (`ver`) that matches a persisted
per-user `TokenVersion`. Every authenticated request re-checks the epoch, so
the following take effect immediately for **all** previously issued tokens:

- logout (`POST /api/logout`, called by the web UI before clearing local state);
- password changes and admin password resets;

Changing your own password (or an admin resetting it) ends **all** of that
user's sessions, including the session used to make the change: the next
request with the old token returns `401` and the UI returns to the login page.
This is deliberate — a password change must not leave other sessions alive.
- permission, scope, and lock-password changes;
- any update through the CLI or API that touches those fields.

Existing tokens issued before this fork keep working until the first
revocation (their epoch is zero). Renewal cannot resurrect a revoked session.
Tokens are still self-contained: keep `tokenExpirationTime` short, and treat a
leaked token as valid until it expires or is revoked.

## Self-registration permissions

Self-registered users get **download only** by default. The configured user
defaults (which include the destructive permissions) are never inherited by
signup. An administrator can opt in to additional rights with the
`signupPermissions` settings block (set through `filebrowser config import`
or the settings API; there is no dedicated CLI flag); `admin` and `execute`
are always withheld and must be granted through a separate admin action.

Signup also requires a derivable scope: enable `createUserDir` for per-user
homes, or configure a non-root default scope. With neither, the server refuses
to start and the signup endpoint returns `403`.

## Hook Authentication

The Hook Authentication method in FileBrowser allows developers to delegate user authentication to an external script or program. Instead of validating credentials internally, FileBrowser sends the username and password to a custom command defined by the administrator. This command receives the credentials through environment variables and returns key‑value pairs indicating whether the user should be authenticated, blocked, or passed through.

The hook’s output controls user permissions, scope, locale, and other attributes, making it a powerful and extensible authentication mechanism.

> [!WARNING]
>
> The submitted username and password are attacker-controlled and are handed to your hook command as the `USERNAME` and `PASSWORD` environment variables. File Browser runs the command directly, without a shell, so the values themselves are inert. However, your script must treat them as untrusted: always quote them (`"$USERNAME"`, `"$PASSWORD"`) and never pass them unquoted to a shell, `eval`, `bash -c`, command substitution, or backticks. A hook script that shell-evaluates these values turns any login request into remote code execution.

For example, the following code delegates filebrowser authentication to a PowerShell script on Windows. You can configure any command (for example, a script in Python, Node.js, etc.).

```sh
filebrowser config set --auth.method=hook --auth.command="powershell.exe -File C:\route\to\your\script\auth.ps1"
```

This is the code for the auth.ps1 script

```sh
param()

# Get FileBrowser credentials from environment variables
$username = $env:USERNAME
$password = $env:PASSWORD

# Users dictionary (for testing purposes only)
$users = @{
    "admin" = "kideW48v7-SdE*"
    "test"  = "2sDd3-etrytñK"
}

# Check if the user exists in the dictionary and verify the password
if ($users.ContainsKey($username) -and $users[$username] -eq $password) {

    # Successful authentication
    Write-Output "hook.action=auth"        # Hook action (in this case, "auth", is required for successful authentication)

    Write-Output "user.perm.admin=true"    # Set admin role (all permissions)
    #You can also define specific permissions like this:
    Write-Output "user.perm.execute=true"
    Write-Output "user.perm.create=true"
    Write-Output "user.perm.rename=true"
    Write-Output "user.perm.modify=true"
    Write-Output "user.perm.delete=true"
    Write-Output "user.perm.share=true"
    Write-Output "user.perm.download=true"

    Write-Output "user.locale=es"          # Set language
    Write-Output "user.viewMode=list"      # Set view mode
    Write-Output "user.scope=/"            # Set FileBrowser scope
    Write-Output "user.singleClick=true"   # Set single click user configuration
    Write-Output "user.hideDotfiles=false" # Set hide dot files user configuration

    #Set other configuration
} else {
    # Block authentication
    Write-Output "hook.action=block"
}
```

### Hook Output Format

A hook authentication script must output a series of key–value pairs, one per line, using the format:

```
key=value
```

FileBrowser reads these lines and applies the corresponding authentication action and user configuration.

#### Required Fields

The hook must output one of the following actions:

| Key    | Description |
|--------|------------ |
| hook.action=auth | Authenticates the user. FileBrowser will create or update the user if needed. |
| hook.action=block | Rejects authentication. The login attempt fails. |
| hook.action=pass | Delegates authentication to FileBrowser’s internal password validation. |

For most custom authentication flows, auth or block are used.

Example of a successful authentication:

```sh
hook.action=auth
```

#### Optional User Fields

When `hook.action=auth` is returned, the hook may also define additional user attributes. These fields override FileBrowser defaults and allow full customization of the authenticated user.

1. Permissions
```
user.perm.admin=true
user.perm.execute=true
user.perm.create=true
user.perm.rename=true
user.perm.modify=true
user.perm.delete=true
user.perm.share=true
user.perm.download=true
```
> Setting user.perm.admin=true automatically enables all permissions.

2. User Interface and Behavior
```
user.locale=es
user.viewMode=list
user.singleClick=true
user.hideDotfiles=false
```

3. User Scope
```
user.scope=/
```

A scope returned by the hook is trusted as administrator intent: the hook is
your own script, so an explicit scope — including the root — is accepted, and
several users may share it. (Self-signup and proxy auto-provisioning never
pass an explicit scope, so they can never provision the server root.)

## No Authentication

We also provide a no authentication mechanism for users that want to use File Browser privately such in a home network. By setting this authentication method, the user with **id 1** will be used as the default users. Creating more users won't have any effect.

```sh
filebrowser config set --auth.method=noauth
```
