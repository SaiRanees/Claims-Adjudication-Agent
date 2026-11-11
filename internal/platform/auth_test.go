package platform_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

var (
	testSecret = []byte("test-secret-at-least-32-bytes-long!!")
	testNow    = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
)

func fixedClock() time.Time { return testNow }

// sign builds a token directly with golang-jwt, independently of the code under test.
func sign(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("signing test token: %v", err)
	}
	return token
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub":  "alice",
		"role": "submitter",
		"aud":  "claims-api",
		"iat":  testNow.Add(-time.Minute).Unix(),
		"exp":  testNow.Add(time.Hour).Unix(),
	}
}

func with(changes map[string]any, drop ...string) jwt.MapClaims {
	c := validClaims()
	for k, v := range changes {
		c[k] = v
	}
	for _, k := range drop {
		delete(c, k)
	}
	return c
}

func TestAuthenticateAcceptsValidToken(t *testing.T) {
	auth, err := platform.NewAuthenticator(testSecret, fixedClock)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		role string
		want platform.Principal
	}{
		{"submitter", "submitter", platform.Principal{Subject: "alice", Role: platform.RoleSubmitter}},
		{"reviewer", "reviewer", platform.Principal{Subject: "alice", Role: platform.RoleReviewer}},
		{"admin", "admin", platform.Principal{Subject: "alice", Role: platform.RoleAdmin}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := sign(t, jwt.SigningMethodHS256, testSecret, with(map[string]any{"role": tc.role}))
			got, err := auth.Authenticate(token)
			if err != nil {
				t.Fatalf("Authenticate: %v", err)
			}
			if got != tc.want {
				t.Fatalf("principal = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAuthenticateRejectsBadTokens(t *testing.T) {
	auth, err := platform.NewAuthenticator(testSecret, fixedClock)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"not a JWT", "not.a.token"},
		{"signed with another secret", sign(t, jwt.SigningMethodHS256, []byte("another-secret-that-is-32-bytes-long"), validClaims())},
		{"alg none", sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims())},
		{"HS512 with the right secret", sign(t, jwt.SigningMethodHS512, testSecret, validClaims())},
		{"expired an hour ago", sign(t, jwt.SigningMethodHS256, testSecret, with(map[string]any{"exp": testNow.Add(-time.Hour).Unix()}))},
		{"no expiry", sign(t, jwt.SigningMethodHS256, testSecret, with(nil, "exp"))},
		{"not valid yet", sign(t, jwt.SigningMethodHS256, testSecret, with(map[string]any{"nbf": testNow.Add(time.Hour).Unix()}))},
		{"another audience", sign(t, jwt.SigningMethodHS256, testSecret, with(map[string]any{"aud": "some-other-api"}))},
		{"no audience", sign(t, jwt.SigningMethodHS256, testSecret, with(nil, "aud"))},
		{"no subject", sign(t, jwt.SigningMethodHS256, testSecret, with(nil, "sub"))},
		{"subject over 200 characters", sign(t, jwt.SigningMethodHS256, testSecret, with(map[string]any{"sub": strings.Repeat("a", 201)}))},
		{"unknown role", sign(t, jwt.SigningMethodHS256, testSecret, with(map[string]any{"role": "superuser"}))},
		{"no role", sign(t, jwt.SigningMethodHS256, testSecret, with(nil, "role"))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := auth.Authenticate(tc.token)
			if !errors.Is(err, platform.ErrInvalidToken) {
				t.Fatalf("Authenticate = (%+v, %v), want ErrInvalidToken", got, err)
			}
		})
	}
}

func TestNewAuthenticatorRejectsShortSecret(t *testing.T) {
	if _, err := platform.NewAuthenticator([]byte("only-31-bytes-long-secret-value"), fixedClock); err == nil {
		t.Fatal("NewAuthenticator accepted a 31-byte secret")
	}
}

func TestIssuedTokenAuthenticatesUntilItExpires(t *testing.T) {
	now := testNow
	clock := func() time.Time { return now }
	auth, err := platform.NewAuthenticator(testSecret, clock)
	if err != nil {
		t.Fatal(err)
	}
	want := platform.Principal{Subject: "reviewer-7", Role: platform.RoleReviewer}
	token, err := auth.Issue(want, 10*time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := auth.Authenticate(token)
	if err != nil || got != want {
		t.Fatalf("Authenticate(issued) = (%+v, %v), want (%+v, nil)", got, err, want)
	}
	now = testNow.Add(time.Hour)
	if _, err := auth.Authenticate(token); !errors.Is(err, platform.ErrInvalidToken) {
		t.Fatalf("Authenticate an hour later: err = %v, want ErrInvalidToken", err)
	}
}

func TestIssueRejectsInvalidPrincipal(t *testing.T) {
	auth, err := platform.NewAuthenticator(testSecret, fixedClock)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []platform.Principal{
		{Subject: "", Role: platform.RoleSubmitter},
		{Subject: "alice", Role: "superuser"},
	} {
		if _, err := auth.Issue(p, time.Minute); err == nil {
			t.Errorf("Issue(%+v) succeeded, want an error", p)
		}
	}
}
