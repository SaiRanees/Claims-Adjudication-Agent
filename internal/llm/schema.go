package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// Schema bounds. Google publishes no numeric limits for response schemas,
// only that optional properties, enum values and anyOf branches add to a
// schema's complexity and that one too large or too deep is a 400 (ADR
// 0016). These are our own, close to what line_items.v1 needs; the live
// smoke test proves Google accepts each real schema.
const (
	MaxOptional   = 0  // every property is required (ADR 0015)
	MaxNullable   = 16 // properties whose type includes null
	MaxEnumValues = 64 // summed over the schema
	MaxProperties = 64 // summed over every object
	MaxDepth      = 5  // nesting of objects and arrays, the root being 1
)

// SchemaStats counts what the bounds limit.
type SchemaStats struct {
	Properties int
	Optional   int
	Nullable   int
	EnumValues int
	Depth      int
}

// The keywords a schema may use. Anything else, including anyOf, is
// refused, so every schema is one each provider's structured output can take.
var schemaKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true,
	"items": true, "enum": true, "description": true, "minItems": true, "maxItems": true,
}

type node struct {
	types    []string // e.g. ["string"] or ["string", "null"]
	props    map[string]*node
	order    []string
	required []string
	items    *node
	enum     []string
	minItems int
	maxItems int // -1: none
}

// CheckSchema parses a schema in the accepted subset and holds it to the
// bounds.
func CheckSchema(schemaJSON []byte) (SchemaStats, error) {
	root, err := parseSchema(schemaJSON)
	if err != nil {
		return SchemaStats{}, err
	}
	var st SchemaStats
	count(root, 1, &st)
	switch {
	case st.Optional > MaxOptional:
		return st, fmt.Errorf("llm: schema has %d optional properties, at most %d", st.Optional, MaxOptional)
	case st.Nullable > MaxNullable:
		return st, fmt.Errorf("llm: schema has %d nullable properties, at most %d", st.Nullable, MaxNullable)
	case st.EnumValues > MaxEnumValues:
		return st, fmt.Errorf("llm: schema has %d enum values, at most %d", st.EnumValues, MaxEnumValues)
	case st.Properties > MaxProperties:
		return st, fmt.Errorf("llm: schema has %d properties, at most %d", st.Properties, MaxProperties)
	case st.Depth > MaxDepth:
		return st, fmt.Errorf("llm: schema nests %d deep, at most %d", st.Depth, MaxDepth)
	}
	return st, nil
}

func count(n *node, depth int, st *SchemaStats) {
	st.Depth = max(st.Depth, depth)
	st.EnumValues += len(n.enum)
	for _, name := range n.order {
		p := n.props[name]
		st.Properties++
		if !slices.Contains(n.required, name) {
			st.Optional++
		}
		if slices.Contains(p.types, "null") {
			st.Nullable++
		}
		count(p, depth+1, st)
	}
	if n.items != nil {
		count(n.items, depth+1, st)
	}
}

func parseSchema(b []byte) (*node, error) {
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("llm: schema isn't a JSON object: %w", err)
	}
	return parseNode(raw, "")
}

func parseNode(raw map[string]any, path string) (*node, error) {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("llm: schema at %q: %s", "/"+path, fmt.Sprintf(format, a...))
	}
	for k := range raw {
		if !schemaKeywords[k] {
			return nil, bad("keyword %q isn't in the accepted subset", k)
		}
	}
	n := &node{maxItems: -1}
	switch t := raw["type"].(type) {
	case string:
		n.types = []string{t}
	case []any:
		for _, v := range t {
			s, ok := v.(string)
			if !ok {
				return nil, bad("type list holds a non-string")
			}
			n.types = append(n.types, s)
		}
		if len(n.types) != 2 || n.types[1] != "null" || n.types[0] == "null" {
			return nil, bad("a type list must be [T, \"null\"]")
		}
	default:
		return nil, bad("every schema needs a type")
	}
	base := n.types[0]
	switch base {
	case "object":
		props, ok := raw["properties"].(map[string]any)
		if !ok || len(props) == 0 {
			return nil, bad("an object needs properties")
		}
		if ap, ok := raw["additionalProperties"]; !ok || ap != false {
			return nil, bad("an object must set additionalProperties to false")
		}
		n.props = map[string]*node{}
		n.order = slices.Sorted(maps.Keys(props))
		for _, name := range n.order {
			pm, ok := props[name].(map[string]any)
			if !ok {
				return nil, bad("property %s isn't a schema", name)
			}
			child, err := parseNode(pm, path+"properties/"+name+"/")
			if err != nil {
				return nil, err
			}
			n.props[name] = child
		}
		req, _ := raw["required"].([]any)
		for _, r := range req {
			s, ok := r.(string)
			if !ok || n.props[s] == nil {
				return nil, bad("required names %v, which isn't a property", r)
			}
			n.required = append(n.required, s)
		}
	case "array":
		im, ok := raw["items"].(map[string]any)
		if !ok {
			return nil, bad("an array needs items")
		}
		child, err := parseNode(im, path+"items/")
		if err != nil {
			return nil, err
		}
		n.items = child
		for key, dst := range map[string]*int{"minItems": &n.minItems, "maxItems": &n.maxItems} {
			if v, ok := raw[key]; ok {
				num, ok := v.(json.Number)
				i, err := num.Int64()
				if !ok || err != nil || i < 0 {
					return nil, bad("%s must be a non-negative integer", key)
				}
				*dst = int(i)
			}
		}
	case "string", "integer", "boolean":
	default:
		return nil, bad("type %q isn't accepted (numbers come as printed strings, ADR 0015)", base)
	}
	if base != "object" && (raw["properties"] != nil || raw["required"] != nil || raw["additionalProperties"] != nil) {
		return nil, bad("object keywords on a %s", base)
	}
	if base != "array" && (raw["items"] != nil || raw["minItems"] != nil || raw["maxItems"] != nil) {
		return nil, bad("array keywords on a %s", base)
	}
	if e, ok := raw["enum"]; ok {
		list, ok := e.([]any)
		if !ok || len(list) == 0 || base != "string" {
			return nil, bad("enum must be a non-empty list on a string")
		}
		for _, v := range list {
			s, ok := v.(string)
			if !ok {
				return nil, bad("enum values must be strings")
			}
			n.enum = append(n.enum, s)
		}
	}
	return n, nil
}

// Decode checks a model's output against its schema and decodes it into v:
// unknown and missing fields fail, types must match exactly, and enum values
// are matched without regard to case, then given the schema's spelling,
// because structured outputs don't guarantee capitalization. The Go type
// must match the schema; DisallowUnknownFields catches a field it lacks.
func Decode(schema Schema, output string, v any) error {
	root, err := parseSchema(schema.JSON)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(output))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("llm: output isn't JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("llm: data after the output's JSON value")
	}
	norm, err := conform(root, doc, "")
	if err != nil {
		return err
	}
	b, err := json.Marshal(norm)
	if err != nil {
		return err
	}
	strict := json.NewDecoder(bytes.NewReader(b))
	strict.DisallowUnknownFields()
	if err := strict.Decode(v); err != nil {
		return fmt.Errorf("llm: output doesn't fit %T: %w", v, err)
	}
	return nil
}

// conform walks a decoded value against its schema node.
func conform(n *node, v any, path string) (any, error) {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("llm: output at %q: %s", "/"+path, fmt.Sprintf(format, a...))
	}
	if v == nil {
		if slices.Contains(n.types, "null") {
			return nil, nil
		}
		return nil, bad("null where a %s is required", n.types[0])
	}
	switch n.types[0] {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return nil, bad("want an object")
		}
		out := map[string]any{}
		for k := range m {
			if n.props[k] == nil {
				return nil, bad("unknown field %q", k)
			}
		}
		for _, name := range n.order {
			val, ok := m[name]
			if !ok {
				if slices.Contains(n.required, name) {
					return nil, bad("missing field %q", name)
				}
				continue
			}
			c, err := conform(n.props[name], val, path+name+"/")
			if err != nil {
				return nil, err
			}
			out[name] = c
		}
		return out, nil
	case "array":
		list, ok := v.([]any)
		if !ok {
			return nil, bad("want an array")
		}
		if len(list) < n.minItems || (n.maxItems >= 0 && len(list) > n.maxItems) {
			return nil, bad("%d items, outside the schema's bounds", len(list))
		}
		out := make([]any, len(list))
		for i, item := range list {
			c, err := conform(n.items, item, fmt.Sprintf("%s%d/", path, i))
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case "string":
		s, ok := v.(string)
		if !ok {
			return nil, bad("want a string")
		}
		if n.enum == nil {
			return s, nil
		}
		for _, e := range n.enum {
			if strings.EqualFold(e, s) {
				return e, nil
			}
		}
		return nil, bad("%q isn't one of the schema's values", s)
	case "integer":
		num, ok := v.(json.Number)
		if !ok {
			return nil, bad("want an integer")
		}
		i, err := num.Int64()
		if err != nil {
			return nil, bad("%s isn't an integer", num)
		}
		return i, nil
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return nil, bad("want a boolean")
		}
		return b, nil
	}
	return nil, bad("unhandled type %s", n.types[0])
}
