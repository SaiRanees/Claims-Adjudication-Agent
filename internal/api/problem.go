package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api/apigen"
)

// problemType is an RFC 9457 problem type: a URI and the title every instance shares.
// The code on each problem says more precisely what went wrong.
type problemType struct{ uri, title string }

var (
	typeValidation       = problemType{"urn:claims-agent:problem:validation", "The request is invalid"}
	typeUnauthenticated  = problemType{"urn:claims-agent:problem:unauthenticated", "Authentication is required"}
	typeForbidden        = problemType{"urn:claims-agent:problem:forbidden", "Not allowed"}
	typeNotFound         = problemType{"urn:claims-agent:problem:not-found", "Not found"}
	typeMethodNotAllowed = problemType{"urn:claims-agent:problem:method-not-allowed", "Method not allowed"}
	typeConflict         = problemType{"urn:claims-agent:problem:conflict", "Conflict"}
	typeTooLarge         = problemType{"urn:claims-agent:problem:too-large", "Request too large"}
	typeUnprocessable    = problemType{"urn:claims-agent:problem:unprocessable", "The request can't be applied"}
	typeInternal         = problemType{"urn:claims-agent:problem:internal", "Internal error"}
	typeNotImplemented   = problemType{"urn:claims-agent:problem:not-implemented", "Not implemented yet"}
	typeUnavailable      = problemType{"urn:claims-agent:problem:unavailable", "Unavailable"}
)

// problem builds a problem. traceId joins it with tracing (week 1, session 6).
func problem(status int32, t problemType, code, detail string) apigen.Problem {
	p := apigen.Problem{Type: t.uri, Title: t.title, Status: status, Code: &code}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

func withErrors(p apigen.Problem, errs ...apigen.FieldError) apigen.Problem {
	if len(errs) > 0 {
		p.Errors = &errs
	}
	return p
}

func validationFailed(errs ...apigen.FieldError) apigen.Problem {
	return withErrors(problem(http.StatusBadRequest, typeValidation, "validation_failed",
		"The request doesn't match the contract. Each entry in errors points at a field."), errs...)
}

func notFoundProblem() apigen.Problem {
	return problem(http.StatusNotFound, typeNotFound, "not_found", "No such resource, or you can't see it.")
}

func writeProblem(w http.ResponseWriter, p apigen.Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(int(p.Status))
	_ = json.NewEncoder(w).Encode(p)
}

// fieldErrors turns kin-openapi's validation errors into the contract's FieldErrors. A body
// field gets a JSON Pointer into the body, such as /claimedAmountPaise, and "" means the
// whole body. A parameter gets /<in>/<name>, such as /header/Idempotency-Key.
func fieldErrors(err error) []apigen.FieldError {
	var out []apigen.FieldError
	for _, e := range flatten(err) {
		var re *openapi3filter.RequestError
		if !errors.As(e, &re) {
			out = append(out, apigen.FieldError{Pointer: "", Detail: e.Error()})
			continue
		}
		base := ""
		if re.Parameter != nil {
			base = "/" + re.Parameter.In + "/" + re.Parameter.Name
		}
		schemaErrs := schemaErrors(re.Err)
		if len(schemaErrs) == 0 {
			out = append(out, apigen.FieldError{Pointer: base, Detail: requestErrorDetail(re)})
			continue
		}
		for _, se := range schemaErrs {
			pointer := base
			if re.Parameter == nil {
				path := se.JSONPointer()
				if name, ok := unsupportedProperty(se); ok {
					path = append(path, name)
				}
				pointer = jsonPointer(path)
			}
			out = append(out, apigen.FieldError{Pointer: pointer, Detail: se.Reason})
		}
	}
	return out
}

// flatten expands kin-openapi's MultiErrors, keeping each RequestError whole.
func flatten(err error) []error {
	//nolint:errorlint // errors.As would reach inside a RequestError's own MultiError and lose which field it belongs to.
	me, ok := err.(openapi3.MultiError)
	if !ok {
		return []error{err}
	}
	var out []error
	for _, e := range me {
		out = append(out, flatten(e)...)
	}
	return out
}

// unsupportedProperty returns the name of the property an "additionalProperties: false"
// error is about. kin-openapi points that error at the object rather than the property
// (its validator doesn't mark the key, unlike every other property error), and keeps
// the name only in the reason, which it formats as `property %q is unsupported`.
func unsupportedProperty(se *openapi3.SchemaError) (string, bool) {
	if se.SchemaField != "properties" {
		return "", false
	}
	quoted, ok := strings.CutPrefix(se.Reason, "property ")
	if !ok {
		return "", false
	}
	if quoted, ok = strings.CutSuffix(quoted, " is unsupported"); !ok {
		return "", false
	}
	name, err := strconv.Unquote(quoted)
	if err != nil {
		return "", false
	}
	return name, true
}

func schemaErrors(err error) []*openapi3.SchemaError {
	var out []*openapi3.SchemaError
	for _, e := range flatten(err) {
		var se *openapi3.SchemaError
		if errors.As(e, &se) {
			out = append(out, se)
		}
	}
	return out
}

func requestErrorDetail(re *openapi3filter.RequestError) string {
	switch {
	case errors.Is(re.Err, openapi3filter.ErrInvalidRequired):
		return "is required"
	case re.Reason != "":
		return re.Reason
	case re.Err != nil:
		return re.Err.Error()
	}
	return "is invalid"
}

var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

func jsonPointer(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString("/")
		b.WriteString(pointerEscaper.Replace(p))
	}
	return b.String()
}
