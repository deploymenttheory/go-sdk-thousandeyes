package codegen

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// apiPrefix matches the namespace the unified specification prefixes every
// component with — "Tests_API_BgpTest", "Alerts_API_Link". The unified document
// merges 27 per-domain specifications, and each keeps its own component
// namespace, so the same concept appears once per domain. The prefix carries no
// information inside a package that already belongs to that domain, so it is
// stripped from generated type names.
var apiPrefix = regexp.MustCompile(`^[A-Za-z0-9]+(_[A-Za-z0-9]+)*_API_`)

// nonAlphanumeric matches everything that cannot appear in a Go identifier.
var nonAlphanumeric = regexp.MustCompile(`[^A-Za-z0-9]+`)

// goIdentifier converts an arbitrary spec name into an exported Go identifier.
func goIdentifier(s string) string {
	s = apiPrefix.ReplaceAllString(s, "")
	parts := nonAlphanumeric.Split(s, -1)

	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			b.WriteString(p[1:])
		}
	}

	out := b.String()
	if out == "" {
		return "Value"
	}
	// An identifier cannot begin with a digit.
	if out[0] >= '0' && out[0] <= '9' {
		out = "N" + out
	}
	return out
}

// goFieldName converts a JSON property name into an exported Go field name,
// fixing up the initialisms Go convention capitalises.
func goFieldName(s string) string {
	name := goIdentifier(s)
	for _, initialism := range []string{"Id", "Url", "Uri", "Api", "Http", "Https", "Dns", "Ip", "Ssl", "Bgp", "Sip", "Ftp", "Mtu", "Aid"} {
		if name == initialism {
			return strings.ToUpper(initialism)
		}
		if strings.HasSuffix(name, initialism) {
			name = strings.TrimSuffix(name, initialism) + strings.ToUpper(initialism)
		}
	}
	return name
}

// goKeywords are the reserved words a generated identifier must not collide
// with. The specification names a path parameter "type", among others.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
	// Predeclared identifiers worth avoiding as parameter names.
	"len": true, "cap": true, "new": true, "make": true, "copy": true,
	"append": true, "string": true, "error": true, "ctx": true, "request": true,
	"result": true, "endpoint": true, "req": true, "resp": true, "err": true,
	"opts": true, "opt": true, "s": true,
}

// safeParamName returns an identifier safe to use as a function parameter.
func safeParamName(s string) string {
	if goKeywords[s] {
		return s + "Param"
	}
	return s
}

// packageName converts a tag such as "BGP Tests" into "bgp_tests", matching the
// snake_case directory convention of go-sdk-jamfpro-v2's jamf_pro_api.
func packageName(tag string) string {
	s := nonAlphanumeric.ReplaceAllString(tag, "_")
	s = strings.Trim(s, "_")
	s = strings.ToLower(s)
	if s == "" {
		return "misc"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "v" + s
	}
	return s
}

// serviceName converts a tag into the exported service type name.
func serviceName(tag string) string { return goIdentifier(tag) }

// methodName converts an operationId such as "getBgpTests" into "GetBgpTests".
func methodName(operationID string) string { return goIdentifier(operationID) }

// TypeResolver turns schemas into Go type expressions, collecting the named
// types it encounters so they can be emitted alongside the service.
type TypeResolver struct {
	spec *Spec
	// models holds the generated definition for each named type.
	models map[string]*Model
	// enums holds the generated enumerations, keyed by type name.
	enums map[string]*Enum
	// unions holds the generated discriminated unions, keyed by type name.
	unions map[string]*Union
	// resolving guards against the recursive references the specification
	// contains — a test references an agent which references a test.
	resolving map[string]bool
	// usesTemplating records whether any field resolved to the Templates
	// substitution generic, so the package imports it only when needed.
	usesTemplating bool
}

// UsesTemplating reports whether any resolved type needs the templating import.
func (r *TypeResolver) UsesTemplating() bool { return r.usesTemplating }

// Model is a named Go type to emit.
type Model struct {
	Name        string
	Description string
	Definition  string
}

// EnumValue is one member of an enumeration.
type EnumValue struct {
	// ConstName is the generated constant identifier.
	ConstName string
	// Literal is the Go literal for the value, quoted for string enums.
	Literal string
	// Display is the value as the API spells it, used by String().
	Display string
}

// UnionVariant is one member of a discriminated union.
type UnionVariant struct {
	// Discriminator is the tag value selecting this variant, e.g. "all-agents".
	Discriminator string
	// FieldName is the generated accessor field.
	FieldName string
	// TypeName is the variant's Go type.
	TypeName string
}

// Union is a oneOf with a discriminator.
//
// Every oneOf in the specification carries a discriminator, so each decodes
// unambiguously by reading one property. The generated type holds the tag plus
// one pointer per variant, and UnmarshalJSON populates exactly the one the tag
// selects. An unrecognised tag is not an error: the raw payload is retained so
// a variant added upstream is still readable.
type Union struct {
	Name          string
	Description   string
	Discriminator string
	// DiscriminatorField is the Go field holding the tag.
	DiscriminatorField string
	// DiscriminatorType is the generated enum the tag is typed as. It is
	// synthesised from the discriminator mapping keys: the specification gives
	// each variant its own single-value tag enum but no union-level one.
	DiscriminatorType string
	Variants          []UnionVariant
}

// DocComment renders the union's doc comment.
func (u Union) DocComment() string {
	lead := u.Name + " is a discriminated union from the OpenAPI specification. "
	body := "The " + u.Discriminator + " property selects which variant is populated."
	if u.Description != "" {
		body = firstSentence(u.Description) + " " + body
	}
	return commentBlock(lead, body)
}

// Enum is a named enumeration.
//
// Enumerations are generated as open types: the underlying scalar is preserved,
// so a value the specification does not list still decodes and compares
// correctly rather than failing. The API adds values over time — several
// enumerations already carry an explicit "unknown" member — and a closed type
// would turn a routine upstream addition into a decode failure.
type Enum struct {
	Name        string
	Description string
	// BaseType is the underlying scalar: string, or an integer type.
	BaseType string
	Values   []EnumValue
}

// DocComment renders the enum's doc comment.
func (e Enum) DocComment() string {
	lead := e.Name + " is an enumeration from the OpenAPI specification. "
	body := "It is open: values outside the enumeration decode and compare as their underlying " + e.BaseType + "."
	if e.Description != "" {
		body = firstSentence(e.Description) + " " + body
	}
	return commentBlock(lead, body)
}

// DocComment renders the model's doc comment.
func (m Model) DocComment() string {
	if m.Description == "" {
		return fmt.Sprintf("// %s is generated from the OpenAPI specification.", m.Name)
	}
	return commentBlock(m.Name+" ", m.Description)
}

// NewTypeResolver returns a resolver over spec.
func NewTypeResolver(spec *Spec) *TypeResolver {
	return &TypeResolver{
		spec:      spec,
		models:    map[string]*Model{},
		enums:     map[string]*Enum{},
		unions:    map[string]*Union{},
		resolving: map[string]bool{},
	}
}

// Models returns the collected named types, sorted by name.
func (r *TypeResolver) Models() []Model {
	out := make([]Model, 0, len(r.models))
	for _, m := range r.models {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Enums returns the collected enumerations, sorted by name.
func (r *TypeResolver) Enums() []Enum {
	out := make([]Enum, 0, len(r.enums))
	for _, e := range r.enums {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Unions returns the collected discriminated unions, sorted by name.
func (r *TypeResolver) Unions() []Union {
	out := make([]Union, 0, len(r.unions))
	for _, u := range r.unions {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GoType returns the Go type expression for a schema, registering any named
// types it depends on.
func (r *TypeResolver) GoType(schema map[string]any) string {
	if schema == nil {
		return "any"
	}

	if ref, ok := schema["$ref"].(string); ok {
		return r.namedType(ref)
	}

	// allOf composes a type from several schemas. Rather than modelling
	// embedding, the members are flattened into one struct, which keeps the
	// generated surface flat and JSON-compatible.
	if members, ok := schema["allOf"].([]any); ok && len(members) > 0 {
		return r.structFromAllOf(members)
	}

	// oneOf and anyOf are unions, which Go cannot express without a wrapper
	// type per combination. They degrade to an untyped value, except for the
	// Templates substitution overlay handled below.
	if _, ok := schema["oneOf"]; ok {
		return "any"
	}
	if members, ok := schema["anyOf"].([]any); ok {
		if templated := r.templatedValue(members); templated != "" {
			return templated
		}
		return "any"
	}

	switch typeOf(schema) {
	case "array":
		items, _ := schema["items"].(map[string]any)
		return "[]" + r.GoType(items)
	case "object":
		return r.inlineStruct(schema)
	case "string":
		return "string"
	case "integer":
		if format, _ := schema["format"].(string); format == "int64" {
			return "int64"
		}
		return "int"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	default:
		return "any"
	}
}

// namedType resolves a $ref, emitting the referenced schema as a named type.
func (r *TypeResolver) namedType(ref string) string {
	schema, rawName, ok := r.spec.Resolve(ref)
	if !ok {
		return "any"
	}

	name := goIdentifier(rawName)

	// Already emitted, or currently being emitted further up the stack. A
	// pointer breaks the recursion for self-referential schemas.
	if _, done := r.models[name]; done {
		return name
	}
	if r.resolving[name] {
		return "*" + name
	}

	if values, ok := schema["enum"].([]any); ok && len(values) > 0 {
		r.registerEnum(name, schema, values)
		return name
	}

	if _, ok := schema["oneOf"].([]any); ok {
		if r.registerUnion(name, schema) {
			return name
		}
	}

	r.resolving[name] = true
	defer delete(r.resolving, name)

	description, _ := schema["description"].(string)
	// Reserve the name before resolving the body so a cycle back to this type
	// sees it as in-progress rather than re-entering.
	r.models[name] = &Model{Name: name, Description: description, Definition: "any"}
	r.models[name].Definition = r.definitionFor(schema)

	return name
}

// definitionFor renders the body of a named type.
func (r *TypeResolver) definitionFor(schema map[string]any) string {
	if members, ok := schema["allOf"].([]any); ok && len(members) > 0 {
		return r.structFromAllOf(members)
	}
	switch typeOf(schema) {
	case "object":
		return r.inlineStruct(schema)
	case "array":
		items, _ := schema["items"].(map[string]any)
		return "[]" + r.GoType(items)
	default:
		return r.GoType(schema)
	}
}

// inlineStruct renders an object schema as a Go struct literal type.
func (r *TypeResolver) inlineStruct(schema map[string]any) string {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		// An object with no declared properties is a free-form map.
		if extra, ok := schema["additionalProperties"].(map[string]any); ok {
			return "map[string]" + r.GoType(extra)
		}
		return "map[string]any"
	}

	required := map[string]bool{}
	if raw, ok := schema["required"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				required[s] = true
			}
		}
	}

	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("struct {\n")
	for _, jsonName := range names {
		propSchema, _ := props[jsonName].(map[string]any)
		goType := r.GoType(propSchema)

		tag := jsonName
		if !required[jsonName] {
			tag += ",omitempty"
			// Optional scalars take a pointer so an unset value is
			// distinguishable from a zero one, which matters on PATCH.
			if isScalar(goType) {
				goType = "*" + goType
			}
		}

		if desc, _ := propSchema["description"].(string); desc != "" {
			b.WriteString("\t" + commentBlock("", firstSentence(desc)) + "\n")
		}
		fmt.Fprintf(&b, "\t%s %s `json:\"%s\"`\n", goFieldName(jsonName), goType, tag)
	}
	b.WriteString("}")
	return b.String()
}

// structFromAllOf flattens the members of an allOf into a single struct.
func (r *TypeResolver) structFromAllOf(members []any) string {
	// A single-member allOf is not composition: the specification uses it to
	// attach a description to a $ref. The member may be a scalar or an
	// enumeration, so delegating preserves the named type instead of
	// collapsing it to a free-form map.
	if len(members) == 1 {
		if only, ok := members[0].(map[string]any); ok {
			if _, isObject := only["properties"]; !isObject {
				if _, composes := only["allOf"]; !composes {
					return r.GoType(only)
				}
			}
		}
	}

	props := map[string]any{}
	required := map[string]bool{}
	r.collectAllOf(members, props, required, 0)

	if len(props) == 0 {
		if first, ok := members[0].(map[string]any); ok {
			return r.GoType(first)
		}
		return "map[string]any"
	}

	req := make([]any, 0, len(required))
	for name := range required {
		req = append(req, name)
	}

	return r.inlineStruct(map[string]any{
		"type":       "object",
		"properties": props,
		"required":   req,
	})
}

// maxCompositionDepth bounds the allOf walk. The specification composes base
// schemas several levels deep and could in principle cycle.
const maxCompositionDepth = 12

// collectAllOf gathers properties from an allOf chain.
//
// Composition nests: a test schema references a base schema which is itself an
// allOf over further bases, so merging only the immediate members yields a type
// carrying almost none of its fields. Each member is followed to the bottom.
func (r *TypeResolver) collectAllOf(
	members []any,
	props map[string]any,
	required map[string]bool,
	depth int,
) {
	if depth > maxCompositionDepth {
		return
	}

	for _, raw := range members {
		member, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if ref, isRef := member["$ref"].(string); isRef {
			resolved, _, found := r.spec.Resolve(ref)
			if !found {
				continue
			}
			member = resolved
		}

		if nested, composes := member["allOf"].([]any); composes {
			r.collectAllOf(nested, props, required, depth+1)
		}

		if p, has := member["properties"].(map[string]any); has {
			for k, v := range p {
				props[k] = v
			}
		}
		if reqs, has := member["required"].([]any); has {
			for _, v := range reqs {
				if name, ok := v.(string); ok {
					required[name] = true
				}
			}
		}
	}
}

func typeOf(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case string:
		return t
	case []any:
		// A nullable union such as ["string","null"]: take the first concrete type.
		for _, v := range t {
			if s, ok := v.(string); ok && s != "null" {
				return s
			}
		}
	}
	// A schema with properties but no declared type is still an object.
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	return ""
}

func isScalar(goType string) bool {
	switch goType {
	case "string", "int", "int64", "float64", "bool":
		return true
	}
	return false
}

// registerEnum records a named enumeration.
func (r *TypeResolver) registerEnum(name string, schema map[string]any, values []any) {
	if _, done := r.enums[name]; done {
		return
	}

	description, _ := schema["description"].(string)
	base := "string"
	if typeOf(schema) == "integer" {
		base = "int"
		if format, _ := schema["format"].(string); format == "int64" {
			base = "int64"
		}
	}

	enum := &Enum{Name: name, Description: description, BaseType: base}
	seen := map[string]bool{}

	for _, raw := range values {
		display := fmt.Sprintf("%v", raw)

		var literal string
		if base == "string" {
			literal = fmt.Sprintf("%q", display)
		} else {
			literal = display
		}

		constName := name + goIdentifier(display)
		if base != "string" {
			// A numeric member cannot contribute an identifier of its own, so
			// it is suffixed with the value: TestIntervalN60.
			constName = name + "N" + nonAlphanumeric.ReplaceAllString(display, "")
		}
		for n := 2; seen[constName]; n++ {
			constName = fmt.Sprintf("%s%d", constName, n)
		}
		seen[constName] = true

		enum.Values = append(enum.Values, EnumValue{
			ConstName: constName,
			Literal:   literal,
			Display:   display,
		})
	}

	r.enums[name] = enum
}

// registerUnion records a discriminated oneOf, reporting whether it did.
//
// A oneOf without a discriminator cannot be decoded unambiguously, so it is
// left to the untyped fallback rather than guessed at. Every oneOf in the
// current specification has one.
func (r *TypeResolver) registerUnion(name string, schema map[string]any) bool {
	if _, done := r.unions[name]; done {
		return true
	}

	discriminator, ok := schema["discriminator"].(map[string]any)
	if !ok {
		return false
	}
	property, _ := discriminator["propertyName"].(string)
	if property == "" {
		return false
	}
	mapping, _ := discriminator["mapping"].(map[string]any)
	if len(mapping) == 0 {
		return false
	}

	description, _ := schema["description"].(string)
	union := &Union{
		Name:               name,
		Description:        description,
		Discriminator:      property,
		DiscriminatorField: goFieldName(property),
		DiscriminatorType:  name + "Type",
	}

	// Reserve the name before resolving variants: a variant may reference the
	// union it belongs to.
	r.unions[name] = union

	tags := make([]string, 0, len(mapping))
	for tag := range mapping {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	for _, tag := range tags {
		ref, _ := mapping[tag].(string)
		if ref == "" {
			continue
		}
		typeName := r.namedType(ref)
		if typeName == "any" {
			continue
		}
		union.Variants = append(union.Variants, UnionVariant{
			Discriminator: tag,
			FieldName:     goIdentifier(tag),
			TypeName:      strings.TrimPrefix(typeName, "*"),
		})
	}

	if len(union.Variants) == 0 {
		delete(r.unions, name)
		return false
	}

	// Type the tag rather than leaving it a bare string. The enum is built from
	// the mapping keys and rendered by the ordinary enum template, so it is open
	// like every other: a tag added upstream still decodes and compares.
	r.registerEnum(union.DiscriminatorType, map[string]any{
		"type":        "string",
		"description": "selects which variant of " + name + " is populated.",
	}, tagValues(tags))

	return true
}

// tagValues adapts discriminator keys to the shape registerEnum expects.
func tagValues(tags []string) []any {
	out := make([]any, 0, len(tags))
	for _, t := range tags {
		out = append(out, t)
	}
	return out
}

// handlebarsSchemaSuffix identifies the Templates API's expression schema.
const handlebarsSchemaSuffix = "HandlebarsExpression"

// templatedValue recognises the Templates API's "value or expression" overlay
// and returns the generic that models it, or an empty string when the anyOf is
// something else.
//
// The overlay is an anyOf over exactly one concrete schema and
// HandlebarsExpression. It is not polymorphism — the field holds a value of a
// known type, or a template that resolves to one — so a single generic covers
// all 147 sites rather than a generated union per field.
func (r *TypeResolver) templatedValue(members []any) string {
	if len(members) != 2 {
		return ""
	}

	var concrete map[string]any
	var sawExpression bool

	for _, raw := range members {
		member, ok := raw.(map[string]any)
		if !ok {
			return ""
		}
		if ref, isRef := member["$ref"].(string); isRef &&
			strings.HasSuffix(ref, handlebarsSchemaSuffix) {
			sawExpression = true
			continue
		}
		if concrete != nil {
			// Two concrete members: a genuine union, not the overlay.
			return ""
		}
		concrete = member
	}

	if !sawExpression || concrete == nil {
		return ""
	}

	inner := r.GoType(concrete)
	if inner == "any" {
		return ""
	}
	r.usesTemplating = true
	return "templating.TemplatedValue[" + strings.TrimPrefix(inner, "*") + "]"
}
