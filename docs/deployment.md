## Self-Registration (Signup)

File Browser allows you to enable user self-registration (signup). This can be enabled via **Settings → Global Settings**, or with `filebrowser config set --signup`.

Self-registered users get **download-only** permissions by default; the
configured user defaults are not inherited. Additional rights require the
explicit `signupPermissions` settings block (see
[authentication](authentication.md#self-registration-permissions)).

Signup requires a derivable scope. Either:

a. Enable `createUserDir` so each user gets their own directory; or
b. If users are meant to share files, set the default scope to something other than the root.

> [!IMPORTANT]
>
> With signup enabled and neither of the above configured, the server refuses
> to start, and the signup endpoint returns `403`. A self-registered user can
> never be provisioned with the server root as their scope.

## Fail2ban

File Browser does not natively support protection against brute force attacks. Therefore, we suggest using something like [fail2ban](https://github.com/fail2ban/fail2ban), which takes care of that by tracking the logs of your File Browser instance. For more information on how fail2ban works, please refer to their [wiki](https://github.com/fail2ban/fail2ban/wiki).

### Filter Configuration

An example filter configuration targeted at matching File Browser's logs.

```ini
[INCLUDES]
before = common.conf

[Definition]
datepattern = `^%%Y\/%%m\/%%d %%H:%%M:%%S`
failregex   = `\/api\/login: 403 <HOST> *`
```

### Jail Configuration

An example jail configuration. You should fill it with the path of the logs of File Browser, as well as the port where it is running at.

```ini
[filebrowser]

enabled = true
port = [your_port]
filter = filebrowser
logpath = [your_log_path]
maxretry = 10
bantime = 10m
findtime = 10m
banaction = iptables-allports
banaction_allports = iptables-allports
```
