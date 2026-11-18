package rules

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The errors ParsePack returns. Each comes wrapped in a *FieldError that names
// the field, so check them with errors.Is.
var (
	ErrSyntax            = errors.New("not valid YAML")
	ErrMultipleDocuments = errors.New("more than one YAML document")
	ErrAnchor            = errors.New("anchors are not allowed")
	ErrAlias             = errors.New("aliases are not allowed")
	ErrMergeKey          = errors.New("merge keys are not allowed")
	ErrTag               = errors.New("explicit tags are not allowed")
	ErrUnknownField      = errors.New("unknown field")
	ErrMissingField      = errors.New("missing field")
	ErrDuplicateKey      = errors.New("duplicate key")
	ErrType              = errors.New("wrong type")
	ErrFloat             = errors.New("floats are not allowed")
	ErrInteger           = errors.New("not an integer of the form ^(0|[1-9][0-9]*)$")
	ErrInvalidValue      = errors.New("invalid value")
	ErrDuplicateID       = errors.New("duplicate ID")
	ErrUndefinedClause   = errors.New("cites an undefined clause")
	ErrNoClause          = errors.New("cites no clause and isn't marked unmodelled")
	ErrStep              = errors.New("step unknown, repeated or missing")
	ErrTarget            = errors.New("target unknown, repeated or missing")
	ErrCategory          = errors.New("category unknown, listed twice or not listed")
)

// FieldError is a rule pack error at one field.
type FieldError struct {
	// Path names the field, such as steps[3].rules[0].ceiling_paise. It is
	// empty for an error about the whole document.
	Path string
	// Line is the line of the field, or of its parent mapping when the field
	// is missing. It is zero when unknown.
	Line   int
	Err    error
	Detail string
}

func (e *FieldError) Error() string {
	var b strings.Builder
	b.WriteString("rule pack")
	if e.Path != "" {
		b.WriteString(": ")
		b.WriteString(e.Path)
	}
	if e.Line > 0 {
		fmt.Fprintf(&b, " (line %d)", e.Line)
	}
	b.WriteString(": ")
	b.WriteString(e.Err.Error())
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

func (e *FieldError) Unwrap() error { return e.Err }

func fail(path string, n *yaml.Node, err error, format string, args ...any) error {
	fe := &FieldError{Path: path, Err: err, Detail: fmt.Sprintf(format, args...)}
	if n != nil {
		fe.Line = n.Line
	}
	return fe
}

var (
	canonicalInt = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	kebabID      = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	// Runs of letters, digits and brackets, joined by single spaces, dots,
	// slashes or hyphens: "C.I.1(i)", "C.I.1 Note 2", "Excl01".
	clauseID   = regexp.MustCompile(`^[A-Za-z0-9()]+([ ./-][A-Za-z0-9()]+)*$`)
	uinPattern = regexp.MustCompile(`^[A-Z0-9]{1,64}$`)
)

// ParsePack parses and validates a rule pack. It takes bytes, so the engine
// stays pure, and it is strict: an unknown field, a float, an integer not of
// the form ^(0|[1-9][0-9]*)$, a second document, an anchor, an alias, a merge
// key, an explicit tag, an undefined or missing clause, or a step, target or
// category that is missing, repeated or unknown is an error. Every error is a
// *FieldError naming the field.
func ParsePack(src []byte) (*Pack, error) {
	root, err := decodeOne(src)
	if err != nil {
		return nil, err
	}
	if err := checkNodes(root); err != nil {
		return nil, err
	}
	p, err := decodePack(root)
	if err != nil {
		return nil, err
	}
	p.sha256 = sha256.Sum256(src)
	return p, nil
}

// decodeOne decodes the only document in src and returns its root node.
func decodeOne(src []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fail("", nil, ErrMissingField, "the pack is empty")
		}
		return nil, fail("", nil, ErrSyntax, "%v", err)
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		return nil, fail("", &next, ErrMultipleDocuments, "a pack is one document")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, fail("", &doc, ErrSyntax, "want one document")
	}
	return doc.Content[0], nil
}

// checkNodes rejects aliases, then anchors, merge keys and explicit tags,
// anywhere in the tree. Aliases get a pass of their own because an alias
// always follows its anchor, so a single pass in document order would report
// every alias as an anchor.
func checkNodes(root *yaml.Node) error {
	err := walk("", root, false, func(path string, n *yaml.Node, _ bool) error {
		if n.Kind == yaml.AliasNode {
			return fail(path, n, ErrAlias, "*%s", n.Value)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return walk("", root, false, func(path string, n *yaml.Node, isKey bool) error {
		switch {
		case n.Anchor != "":
			return fail(path, n, ErrAnchor, "&%s", n.Anchor)
		case n.Style&yaml.TaggedStyle != 0:
			return fail(path, n, ErrTag, "%s", n.Tag)
		case isKey && n.Kind == yaml.ScalarNode && n.Value == "<<" && isPlain(n):
			return fail(path, n, ErrMergeKey, "<<")
		}
		return nil
	})
}

// walk calls visit on n and every node below it, keys before their values,
// in document order. It doesn't follow aliases.
func walk(path string, n *yaml.Node, isKey bool, visit func(path string, n *yaml.Node, isKey bool) error) error {
	if err := visit(path, n, isKey); err != nil {
		return err
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			kp := join(path, k.Value)
			if err := walk(kp, k, true, visit); err != nil {
				return err
			}
			if err := walk(kp, v, false, visit); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			if err := walk(index(path, i), c, false, visit); err != nil {
				return err
			}
		}
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
	}
	return nil
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func index(path string, i int) string { return fmt.Sprintf("%s[%d]", path, i) }

func isPlain(n *yaml.Node) bool {
	return n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0
}

func kindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return "a single value"
	case yaml.AliasNode:
		return "an alias"
	case yaml.DocumentNode:
		return "a document"
	}
	return "nothing"
}

// fields is a decoded mapping: its keys, checked for duplicates, and their
// values.
type fields struct {
	path string
	node *yaml.Node
	vals map[string]*yaml.Node
}

func mapping(path string, n *yaml.Node) (*fields, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fail(path, n, ErrType, "want a mapping, got %s", kindName(n))
	}
	f := &fields{path: path, node: n, vals: make(map[string]*yaml.Node, len(n.Content)/2)}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" {
			return nil, fail(path, k, ErrType, "keys must be strings")
		}
		if _, dup := f.vals[k.Value]; dup {
			return nil, fail(join(path, k.Value), k, ErrDuplicateKey, "%q appears twice", k.Value)
		}
		f.vals[k.Value] = v
	}
	return f, nil
}

// allow fails on the first key, in document order, that isn't allowed.
func (f *fields) allow(allowed ...string) error {
	for i := 0; i+1 < len(f.node.Content); i += 2 {
		k := f.node.Content[i]
		if !slices.Contains(allowed, k.Value) {
			return fail(join(f.path, k.Value), k, ErrUnknownField, "allowed here: %s", strings.Join(allowed, ", "))
		}
	}
	return nil
}

func (f *fields) get(key string) (*yaml.Node, string, bool) {
	n, ok := f.vals[key]
	return n, join(f.path, key), ok
}

func (f *fields) need(key string) (*yaml.Node, string, error) {
	n, p, ok := f.get(key)
	if !ok {
		return nil, p, fail(p, f.node, ErrMissingField, "%s is required", key)
	}
	return n, p, nil
}

func (f *fields) str(key string) (string, error) {
	n, p, err := f.need(key)
	if err != nil {
		return "", err
	}
	return str(p, n)
}

// id reads a required string that must match re.
func (f *fields) id(key string, re *regexp.Regexp) (string, *yaml.Node, error) {
	n, p, err := f.need(key)
	if err != nil {
		return "", nil, err
	}
	s, err := str(p, n)
	if err != nil {
		return "", nil, err
	}
	if !re.MatchString(s) {
		return "", nil, fail(p, n, ErrInvalidValue, "%q doesn't match %s", s, re)
	}
	return s, n, nil
}

// intIn reads a required integer from lo to hi.
func (f *fields) intIn(key string, lo, hi int64) (int64, error) {
	n, p, err := f.need(key)
	if err != nil {
		return 0, err
	}
	return intIn(p, n, lo, hi)
}

// optIntIn reads an optional integer from lo to hi.
func (f *fields) optIntIn(key string, lo, hi int64) (int64, bool, error) {
	n, p, ok := f.get(key)
	if !ok {
		return 0, false, nil
	}
	v, err := intIn(p, n, lo, hi)
	return v, err == nil, err
}

func intIn(path string, n *yaml.Node, lo, hi int64) (int64, error) {
	v, err := integer(path, n)
	if err != nil {
		return 0, err
	}
	if v < lo || v > hi {
		return 0, fail(path, n, ErrInvalidValue, "%d is outside %d to %d", v, lo, hi)
	}
	return v, nil
}

func isScalar(path string, n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fail(path, n, ErrType, "want a single value, got %s", kindName(n))
	}
	return nil
}

func str(path string, n *yaml.Node) (string, error) {
	if err := isScalar(path, n); err != nil {
		return "", err
	}
	switch n.ShortTag() {
	case "!!str":
		return n.Value, nil
	case "!!float":
		return "", fail(path, n, ErrFloat, "%s", n.Value)
	}
	return "", fail(path, n, ErrType, "want a string, got %s %s", n.ShortTag(), n.Value)
}

func integer(path string, n *yaml.Node) (int64, error) {
	if err := isScalar(path, n); err != nil {
		return 0, err
	}
	if isPlain(n) && canonicalInt.MatchString(n.Value) {
		v, err := strconv.ParseInt(n.Value, 10, 64)
		if err != nil {
			return 0, fail(path, n, ErrInteger, "%s doesn't fit in 64 bits", n.Value)
		}
		return v, nil
	}
	if n.ShortTag() == "!!float" {
		return 0, fail(path, n, ErrFloat, "%s", n.Value)
	}
	return 0, fail(path, n, ErrInteger, "%q", n.Value)
}

func boolean(path string, n *yaml.Node) (bool, error) {
	if err := isScalar(path, n); err != nil {
		return false, err
	}
	if isPlain(n) {
		switch n.Value {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	if n.ShortTag() == "!!float" {
		return false, fail(path, n, ErrFloat, "%s", n.Value)
	}
	return false, fail(path, n, ErrType, "want true or false, got %q", n.Value)
}

func date(path string, n *yaml.Node) (Date, error) {
	if err := isScalar(path, n); err != nil {
		return Date{}, err
	}
	switch n.ShortTag() {
	case "!!timestamp", "!!str":
	case "!!float":
		return Date{}, fail(path, n, ErrFloat, "%s", n.Value)
	default:
		return Date{}, fail(path, n, ErrType, "want a date, got %s %s", n.ShortTag(), n.Value)
	}
	d, err := ParseDate(n.Value)
	if err != nil {
		return Date{}, fail(path, n, ErrInvalidValue, "%v", err)
	}
	return d, nil
}

func sequence(path string, n *yaml.Node) ([]*yaml.Node, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fail(path, n, ErrType, "want a list, got %s", kindName(n))
	}
	return n.Content, nil
}

// oneOf checks that s is one of the allowed values.
func oneOf[T ~string](path string, n *yaml.Node, s string, allowed []T) (T, error) {
	for _, a := range allowed {
		if string(a) == s {
			return a, nil
		}
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return "", fail(path, n, ErrInvalidValue, "%q isn't one of %s", s, strings.Join(names, ", "))
}
