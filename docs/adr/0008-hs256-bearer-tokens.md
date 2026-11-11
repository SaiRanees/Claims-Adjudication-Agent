# 0008. Authenticate API callers with HS256 bearer tokens

- Status: Accepted
- Date: 2026-11-11

## Context

The contract requires a bearer JWT on every operation except `/healthz`, with three roles: submitter, reviewer and admin. The caller's identity is part of the data: idempotency keys are scoped to it, and `claims.created_by` records it. There is no identity provider yet, and no web app to log in from until week 6, but the API needs real 401 and 403 answers and per-caller idempotency from its first endpoint.

## Decision

The API verifies HS256 JWTs signed with one shared secret, `AUTH_JWT_SECRET` (at least 32 bytes), using golang-jwt/jwt v5. A token is accepted only if:

- its algorithm is HS256, which shuts out `alg: none` and algorithm-confusion tokens;
- it has an expiry, checked with 30 seconds of clock skew allowed;
- its audience includes `claims-api`;
- its `sub`, the caller, is 1 to 200 characters, which is what the database stores;
- its `role` claim is submitter, reviewer or admin.

`internal/platform.Authenticator` does the checking. Handlers see only a `Principal` (subject and role). `go run ./cmd/dev token [role] [subject]` prints a 12-hour token signed with the secret in `.env`, for calling the API locally.

## Consequences

- 401, 403 and per-caller idempotency are real from week 1, and tests use the same verifier as the service.
- Anyone who holds the secret can sign a token for any caller and role. That suits local development and a demo, and it is not enough once there are real users. Changing the secret invalidates every token.
- Moving to an identity provider (asymmetric keys from its JWKS endpoint) changes `platform.Authenticator` alone; the handlers and the contract stay as they are.
- There is still no login endpoint and no sessions; those arrive with the web app.

## Alternatives considered

- **Verify HS256 with the standard library only.** No new dependency, but about 80 lines of security-sensitive code (algorithm pinning, constant-time comparison, expiry handling) to write and maintain ourselves.
- **Defer authentication with a fixed development user.** Faster, but 401, 403 and per-caller idempotency would not be real, and every endpoint would need revisiting later.
- **Asymmetric keys and JWKS now.** The right end state, but it needs an identity provider or key management before there is anything to log in from.
