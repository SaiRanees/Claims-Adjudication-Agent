package platform

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
)

// Role is what a caller may do. Every token carries exactly one.
type Role string

// The three roles (docs/plan/architecture.md, "Auth and roles").
const (
	RoleSubmitter Role = "submitter"
	RoleReviewer  Role = "reviewer"
	RoleAdmin     Role = "admin"
)

func (r Role) valid() bool {
	switch r {
	case RoleSubmitter, RoleReviewer, RoleAdmin:
		return true
	}
	return false
}

// Principal is an authenticated caller: the token's subject and role. The subject is
// what idempotency keys and claims.created_by record, so it must fit the database's
// limit of 1 to 200 characters.
type Principal struct {
	Subject string
	Role    Role
}

func (p Principal) validate() error {
	if n := utf8.RuneCountInString(p.Subject); n < 1 || n > maxSubjectChars {
		return fmt.Errorf("subject must be 1 to %d characters, got %d", maxSubjectChars, n)
	}
	if !p.Role.valid() {
		return fmt.Errorf("unknown role %q", p.Role)
	}
	return nil
}

const (
	// TokenAudience is the audience every API token must name, so a token issued for
	// another service is refused here.
	TokenAudience = "claims-api"
	// MinSecretBytes is the shortest HS256 secret accepted: the length of the hash output.
	MinSecretBytes = 32

	maxSubjectChars = 200
	clockSkew       = 30 * time.Second
)

// ErrInvalidToken is returned, wrapped, for any token that fails verification. The
// wrapped cause is for logs; callers should only tell the client the token is invalid.
var ErrInvalidToken = errors.New("invalid token")

// Authenticator verifies HS256 bearer tokens signed with a shared secret, and issues them
// for local development (`go run ./cmd/dev token`). Handlers see only Principal, so moving
// to an identity provider's public keys later changes this type alone.
type Authenticator struct {
	secret []byte
	now    func() time.Time
	parser *jwt.Parser
}

// tokenClaims are the claims a token carries: the registered ones plus the caller's role.
type tokenClaims struct {
	Role Role `json:"role"`
	jwt.RegisteredClaims
}

// Validate runs after golang-jwt has checked the expiry, not-before time and audience.
func (c tokenClaims) Validate() error {
	return Principal{Subject: c.Subject, Role: c.Role}.validate()
}

// NewAuthenticator returns an Authenticator for the secret, reading the time from now.
func NewAuthenticator(secret []byte, now func() time.Time) (*Authenticator, error) {
	if len(secret) < MinSecretBytes {
		return nil, fmt.Errorf("the token secret must be at least %d bytes, got %d", MinSecretBytes, len(secret))
	}
	a := &Authenticator{secret: append([]byte(nil), secret...), now: now}
	a.parser = jwt.NewParser(
		// Pinning the algorithm stops "alg: none" and algorithm-confusion tokens.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithAudience(TokenAudience),
		jwt.WithLeeway(clockSkew),
		jwt.WithTimeFunc(now),
	)
	return a, nil
}

// Authenticate verifies a token and returns its caller. Every failure wraps ErrInvalidToken.
func (a *Authenticator) Authenticate(token string) (Principal, error) {
	var claims tokenClaims
	if _, err := a.parser.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) {
		return a.secret, nil
	}); err != nil {
		return Principal{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	return Principal{Subject: claims.Subject, Role: claims.Role}, nil
}

// Issue signs a token for p that expires after ttl.
func (a *Authenticator) Issue(p Principal, ttl time.Duration) (string, error) {
	if err := p.validate(); err != nil {
		return "", fmt.Errorf("issuing a token: %w", err)
	}
	now := a.now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenClaims{
		Role: p.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   p.Subject,
			Audience:  jwt.ClaimStrings{TokenAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	})
	signed, err := token.SignedString(a.secret)
	if err != nil {
		return "", fmt.Errorf("signing a token: %w", err)
	}
	return signed, nil
}
