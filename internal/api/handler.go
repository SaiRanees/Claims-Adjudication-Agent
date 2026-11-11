package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api/apigen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// maxBodyBytes caps request bodies. The contract's largest request is a few hundred bytes.
const maxBodyBytes = 1 << 20

// authChallenge is the WWW-Authenticate header for a missing or rejected token.
const authChallenge = `Bearer realm="claims-api"`

var registerFormats sync.Once

// NewHandler returns the API as an http.Handler. Every request is matched against the
// contract, its bearer token checked where the contract asks for one, and the request
// validated against the contract before a handler sees it. Every error is an RFC 9457
// problem.
func NewHandler(s *Server, auth *platform.Authenticator, log *slog.Logger) (http.Handler, error) {
	registerFormats.Do(func() {
		// The services generate version 7 UUIDs; kin-openapi's RFC 4122 pattern stops at
		// version 5, so it would refuse every ID this system creates.
		openapi3.DefineStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForUUIDOfRFC9562))
	})
	doc, err := apigen.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("loading the embedded contract: %w", err)
	}
	// The contract's one server is "/", whichever host serves the API, so routes are
	// matched on the path alone.
	doc.Servers = nil
	router, err := legacy.NewRouter(doc)
	if err != nil {
		return nil, fmt.Errorf("building the router: %w", err)
	}

	badRequest := func(w http.ResponseWriter, _ *http.Request, err error) {
		writeProblem(w, validationFailed(apigen.FieldError{Pointer: "", Detail: err.Error()}))
	}
	strict := apigen.NewStrictHandlerWithOptions(s, nil, apigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: badRequest,
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, ErrNotImplemented) {
				writeProblem(w, problem(http.StatusNotImplemented, typeNotImplemented, "not_implemented",
					"This operation isn't built yet."))
				return
			}
			log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
			writeProblem(w, problem(http.StatusInternalServerError, typeInternal, "internal_error",
				"Something went wrong on our side."))
		},
	})
	handler := apigen.HandlerWithOptions(strict, apigen.StdHTTPServerOptions{ErrorHandlerFunc: badRequest})
	return &contract{router: router, auth: auth, next: handler}, nil
}

// contract enforces api/openapi.yaml in front of the generated handler.
type contract struct {
	router routers.Router
	auth   *platform.Authenticator
	next   http.Handler
}

func (c *contract) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, pathParams, ok := c.findRoute(r)
	if !ok {
		// The router can't be trusted to tell a wrong method from a wrong path (it only
		// does so for paths without parameters), so ask it which methods the path has.
		if allowed := c.allowedMethods(r); len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			writeProblem(w, problem(http.StatusMethodNotAllowed, typeMethodNotAllowed, "method_not_allowed",
				r.Method+" isn't allowed on this path."))
			return
		}
		writeProblem(w, notFoundProblem())
		return
	}

	if needsBearer(route) {
		caller, ok := c.authenticate(w, r)
		if !ok {
			return
		}
		r = r.WithContext(withPrincipal(r.Context(), caller))
	}

	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	}
	if err := openapi3filter.ValidateRequest(r.Context(), &openapi3filter.RequestValidationInput{
		Request:    r,
		PathParams: pathParams,
		Route:      route,
		Options: &openapi3filter.Options{
			MultiError: true,
			// Tokens were checked above, keeping the caller for the handlers.
			AuthenticationFunc:      openapi3filter.NoopAuthenticationFunc,
			SchemaValidationOptions: []openapi3.SchemaValidationOption{openapi3.EnableFormatValidation()},
		},
	}); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, problem(http.StatusRequestEntityTooLarge, typeTooLarge, "body_too_large",
				fmt.Sprintf("Request bodies are limited to %d bytes.", maxBodyBytes)))
			return
		}
		writeProblem(w, validationFailed(fieldErrors(err)...))
		return
	}
	c.next.ServeHTTP(w, r)
}

// needsBearer reports whether the operation requires a bearer token: its own security
// requirements when it has some (/healthz has an empty list), otherwise the contract's.
func needsBearer(route *routers.Route) bool {
	reqs := route.Spec.Security
	if route.Operation.Security != nil {
		reqs = *route.Operation.Security
	}
	return len(reqs) > 0
}

// authenticate checks the bearer token. On failure it writes the 401 and returns false.
func (c *contract) authenticate(w http.ResponseWriter, r *http.Request) (platform.Principal, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		w.Header().Set("WWW-Authenticate", authChallenge)
		writeProblem(w, problem(http.StatusUnauthorized, typeUnauthenticated, "missing_token",
			"Send a bearer token in the Authorization header."))
		return platform.Principal{}, false
	}
	caller, err := c.auth.Authenticate(strings.TrimSpace(token))
	if err != nil {
		w.Header().Set("WWW-Authenticate", authChallenge+`, error="invalid_token"`)
		writeProblem(w, problem(http.StatusUnauthorized, typeUnauthenticated, "invalid_token",
			"The bearer token is invalid or has expired."))
		return platform.Principal{}, false
	}
	return caller, true
}

// findRoute finds the contract operation for the request. kin-openapi's router is looser
// than the generated ServeMux that dispatches the request: it accepts a trailing slash and
// an empty path parameter, so GET /v1/claims matches GET /v1/claims/{claimId}. A match
// counts only if the operation's path, filled in with the parameters, is the request's path.
func (c *contract) findRoute(r *http.Request) (*routers.Route, map[string]string, bool) {
	route, params, err := c.router.FindRoute(r)
	if err != nil {
		return nil, nil, false
	}
	path := route.Path
	for name, value := range params {
		if value == "" {
			return nil, nil, false
		}
		path = strings.ReplaceAll(path, "{"+name+"}", value)
	}
	if path != r.URL.Path {
		return nil, nil, false
	}
	return route, params, true
}

// allowedMethods lists the methods the contract defines for the request's path.
func (c *contract) allowedMethods(r *http.Request) []string {
	var allowed []string
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, _, ok := c.findRoute(probe); ok {
			allowed = append(allowed, method)
		}
	}
	return allowed
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p platform.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalFrom(ctx context.Context) (platform.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(platform.Principal)
	return p, ok
}
