# GHSA-v7vv-5wj2-gfcj / CVE-2026-54095 — Password reset does not invalidate existing JWT sessions

- Severity: medium (CVSS 5.4)
- CWEs: CWE-613
- Affected: github.com/filebrowser/filebrowser/v2 2.x
- Published: 2026-06-03T09:27:10Z
- URL: https://github.com/filebrowser/filebrowser/security/advisories/GHSA-v7vv-5wj2-gfcj

---

## Summary

File Browser does not invalidate already-issued JWT sessions when a user's password is reset. In a local lab, a user logged in and received a JWT. An administrator then changed that user's password. The old password no longer worked and the new password worked, proving that the password reset succeeded. However, the JWT issued before the password reset continued to authenticate successfully and could still access resources until natural token expiry.

This creates a session revocation gap: resetting a compromised user's password does not immediately evict an attacker who already obtained a valid JWT. This is related to a series of known issues with the current session handling: https://github.com/filebrowser/filebrowser/issues/5216.

## Affected project

- Project: File Browser
- Repository: `https://github.com/filebrowser/filebrowser`
- Tested commit: `ca0108f07097`
- Tested version/tag: `v2.63.5-1-gca0108f0`
- Tested build: binary built from the above checkout

## Vulnerability type

- CWE-613: Insufficient Session Expiration
- CWE-287: Improper Authentication, session lifecycle variant
- Impact: active session remains valid after password reset

## Affected component

- JWT authentication middleware
- User password update flow
- Files of interest:
  - `http/auth.go`
  - `users/storage.go`
  - user update handler paths

## Root cause

File Browser uses signed JWTs for authentication. Password changes update the stored user password, but existing JWTs are not rejected after the change. In the reviewed code path, user updates are tracked in memory and may result in a token-renewal hint, but the request is not reliably rejected. In the live test, the old token remained accepted after the password reset.

A secure session lifecycle should ensure that security-sensitive account changes, such as password reset, invalidate previously issued sessions or compare token issue time against a persisted per-user `validAfter` timestamp.

## Preconditions

The following preconditions were used in the lab:

1. The attacker has obtained a valid JWT for a user account.
2. An administrator resets or changes that user's password.
3. The attacker reuses the previously issued JWT before its natural expiry.

The issue does not require admin privileges from the attacker after the token has been obtained.

## Proof of concept

### Lab setup summary

A local authorized lab instance was created from the tested checkout.

```text
App: http://127.0.0.1:8080
Non-admin test user: emre
Admin user: admin
Protected sanity path: /api/resources/public/
```

### Step 1: User logs in and receives an old JWT

```http
POST /api/login
Content-Type: application/json

{"username":"emre","password":"<old password>"}
```

The response returned a valid JWT. This token is referred to as `OLD_TOKEN` below.

A request using the old token succeeded before the password change:

```http
GET /api/resources/public/
X-Auth: OLD_TOKEN
```

Observed result:

```text
HTTP 200
```

### Step 2: Admin resets the user's password

An administrator changed the user's password through the user update API.

Observed result:

```text
HTTP 200
```

### Step 3: Confirm password change actually took effect

Login with the old password:

```http
POST /api/login

{"username":"emre","password":"<old password>"}
```

Observed result:

```text
HTTP 403
```

Login with the new password:

```http
POST /api/login

{"username":"emre","password":"<new password>"}
```

Observed result:

```text
HTTP 200
```

This confirms that the password change succeeded.

### Step 4: Replay the JWT issued before the password reset

Request using the old JWT after the password reset:

```http
GET /api/resources/public/
X-Auth: OLD_TOKEN
```

Observed result:

```text
HTTP 200
```

The old JWT remained valid after the password reset.

## Impact

If an attacker obtains a user's JWT, resetting the user's password does not immediately revoke the attacker's active session. The attacker can continue using the token until it expires naturally.

Potential scenarios:

- compromised credentials are rotated but the attacker remains logged in,
- password reset during incident response fails to evict active sessions,
- account takeover persistence for the JWT lifetime,
- employee/offboarding or access-reduction workflows may not terminate active sessions as expected.

The impact is bounded by the configured JWT lifetime and by the user's current server-side permissions. However, for compromised-token response, password reset is expected to terminate active sessions.

## Severity

Suggested severity: Medium

Suggested CVSS v3.1:

```text
CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:L/I:L/A:N
```

Alternative, if scoring the attacker as already possessing only a session token and not a normal authenticated account, maintainers may choose a lower score. The operational security impact remains that password reset does not revoke active sessions.

Rationale:

- Network exploitable by replaying an existing token.
- Low attack complexity.
- Requires possession of a valid token.
- No user interaction required.
- Confidentiality and integrity impact are limited to the permissions of the compromised account and token lifetime.

## Recommended remediation

Recommended fixes:

1. Persist a per-user token epoch or `validAfter` timestamp in the database.
2. Include an issued-at (`iat`) timestamp in JWTs and reject any token where `iat < user.validAfter`.
3. Update `validAfter` on password reset, password change, account disablement, and other security-sensitive account changes.
4. Ensure token invalidation survives server restarts.
5. Consider an explicit session/token revocation mechanism for administrators.
6. Add regression tests:

```text
login user -> get OLD_TOKEN
admin resets user password
login old password -> 403
login new password -> 200
GET /api/resources/ with OLD_TOKEN -> should be 401/403
```

## Disclosure notes

This was verified only in an authorized local lab environment. No third-party systems were tested.
