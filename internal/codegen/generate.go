package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/deploymenttheory/go-sdk-thousandeyes/internal/templates"
)

// pathParamPattern matches the {name} placeholders in a path template.
var pathParamPattern = regexp.MustCompile(`\{([^}]+)\}`)

// Param is a path or query parameter.
type Param struct {
	Name        string
	GoName      string
	Description string
	Required    bool
}

// Operation is one generated method.
type Operation struct {
	Name          string
	Method        string
	Path          string
	Summary       string
	Description   string
	PathParams    []Param
	QueryParams   []Param
	HasBody       bool
	BodyType      string
	ResultType    string
	RestyVerb     string
	EndpointExpr  string
	ParamSig      string
	ReturnSig     string
	SuccessReturn string
	// NilPrefix is the leading nil an error return needs when the operation
	// has a result type, so error paths match the signature's arity.
	NilPrefix  string
	DocComment string
}

// Service is one generated package.
type Service struct {
	Package     string
	Name        string
	Tag         string
	SourcePath  string
	SpecVersion string
	// NeedsFmt is false when no operation validates an argument or interpolates
	// a path, in which case importing fmt would not compile.
	NeedsFmt   bool
	Operations []Operation
	Models     []Model
	Enums      []Enum
	Unions     []Union
	// UsesTemplating is true when a model field resolved to the Templates
	// substitution generic, so models.go imports it only when needed.
	UsesTemplating bool
}

// Generator renders services from a specification.
type Generator struct {
	spec      *Spec
	templates *template.Template
}

// NewGenerator returns a generator over spec.
func NewGenerator(spec *Spec) (*Generator, error) {
	tmpl, err := template.ParseFS(templates.FS, "*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	return &Generator{spec: spec, templates: tmpl}, nil
}

// Build assembles a Service for every tag in the specification.
func (g *Generator) Build() []Service {
	byTag := map[string][]RawOperation{}
	for _, op := range g.spec.Operations() {
		byTag[op.Tag] = append(byTag[op.Tag], op)
	}

	tags := make([]string, 0, len(byTag))
	for tag := range byTag {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	services := make([]Service, 0, len(tags))
	for _, tag := range tags {
		// A resolver per service keeps each package's models self-contained,
		// matching how jamf_pro_api gives every service its own models.go.
		resolver := NewTypeResolver(g.spec)

		svc := Service{
			Package:     packageName(tag),
			Name:        serviceName(tag),
			Tag:         tag,
			SourcePath:  filepath.Base(g.spec.Source),
			SpecVersion: g.spec.Version,
		}

		for _, raw := range byTag[tag] {
			svc.Operations = append(svc.Operations, g.buildOperation(raw, resolver))
		}
		svc.Models = resolveNameCollisions(svc.Name, resolver.Models(), &svc.Operations)
		svc.Enums = resolver.Enums()
		svc.Unions = resolver.Unions()
		svc.UsesTemplating = resolver.UsesTemplating()

		for _, op := range svc.Operations {
			if len(op.PathParams) > 0 || op.HasBody {
				svc.NeedsFmt = true
				break
			}
		}

		services = append(services, svc)
	}
	return services
}

// buildOperation turns one spec operation into a renderable method.
func (g *Generator) buildOperation(raw RawOperation, resolver *TypeResolver) Operation {
	op := Operation{
		Name:        methodName(raw.OperationID),
		Method:      raw.Method,
		Path:        raw.Path,
		Summary:     strings.TrimSpace(raw.Summary),
		Description: strings.TrimSpace(raw.Description),
		RestyVerb:   httpMethods[strings.ToLower(raw.Method)],
	}

	op.PathParams, op.QueryParams = g.parameters(raw)
	op.BodyType, op.HasBody = g.requestBody(raw, resolver)
	op.ResultType = g.responseType(raw, resolver)
	op.EndpointExpr = endpointExpression(raw.Path, op.PathParams)

	var sig strings.Builder
	for _, p := range op.PathParams {
		fmt.Fprintf(&sig, ", %s string", p.GoName)
	}
	if op.HasBody {
		fmt.Fprintf(&sig, ", request %s", op.BodyType)
	}
	op.ParamSig = sig.String()

	if op.ResultType != "" {
		op.ReturnSig = fmt.Sprintf("*%s, *resty.Response, error", op.ResultType)
		op.SuccessReturn = "&result, "
		op.NilPrefix = "nil, "
	} else {
		op.ReturnSig = "*resty.Response, error"
		op.SuccessReturn = ""
		op.NilPrefix = ""
	}

	op.DocComment = operationDoc(op)
	return op
}

// parameters splits an operation's declared parameters into path and query.
func (g *Generator) parameters(raw RawOperation) (path, query []Param) {
	params, _ := raw.Op["parameters"].([]any)

	for _, item := range params {
		p, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := p["$ref"].(string); ok {
			resolved, _, found := g.spec.Resolve(ref)
			if !found {
				continue
			}
			p = resolved
		}

		name, _ := p["name"].(string)
		if name == "" {
			continue
		}
		description, _ := p["description"].(string)
		required, _ := p["required"].(bool)

		param := Param{
			Name:        name,
			GoName:      safeParamName(lowerFirst(goIdentifier(name))),
			Description: firstSentence(description),
			Required:    required,
		}

		switch in, _ := p["in"].(string); in {
		case "path":
			path = append(path, param)
		case "query":
			query = append(query, param)
		}
	}

	// aid is applied by the generated code from the client's configuration, so
	// it is not reported as a caller-supplied parameter.
	filtered := query[:0]
	for _, q := range query {
		if q.Name != "aid" {
			filtered = append(filtered, q)
		}
	}
	return path, filtered
}

// requestBody returns the Go type of the request body, if the operation has one.
func (g *Generator) requestBody(raw RawOperation, resolver *TypeResolver) (string, bool) {
	body, ok := raw.Op["requestBody"].(map[string]any)
	if !ok {
		return "", false
	}
	schema := contentSchema(body)
	if schema == nil {
		return "", false
	}

	goType := resolver.GoType(schema)
	if goType == "any" {
		return "any", true
	}
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") {
		return goType, true
	}
	return "*" + goType, true
}

// responseType returns the Go type of the success response, or an empty string
// when the operation returns no body — a 204 on delete, typically.
func (g *Generator) responseType(raw RawOperation, resolver *TypeResolver) string {
	responses, ok := raw.Op["responses"].(map[string]any)
	if !ok {
		return ""
	}

	// Prefer the lowest 2xx that carries content.
	codes := make([]string, 0, len(responses))
	for code := range responses {
		if strings.HasPrefix(code, "2") {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)

	for _, code := range codes {
		response, ok := responses[code].(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := response["$ref"].(string); ok {
			resolved, _, found := g.spec.Resolve(ref)
			if !found {
				continue
			}
			response = resolved
		}
		schema := contentSchema(response)
		if schema == nil {
			continue
		}
		goType := resolver.GoType(schema)
		if goType == "any" {
			continue
		}
		return strings.TrimPrefix(goType, "*")
	}
	return ""
}

// contentSchema pulls the schema out of a requestBody or response, preferring
// HAL since that is what the API predominantly serves.
func contentSchema(holder map[string]any) map[string]any {
	content, ok := holder["content"].(map[string]any)
	if !ok {
		return nil
	}
	for _, mediaType := range []string{"application/hal+json", "application/json"} {
		if media, ok := content[mediaType].(map[string]any); ok {
			if schema, ok := media["schema"].(map[string]any); ok {
				return schema
			}
		}
	}
	return nil
}

// endpointExpression renders the Go expression producing the request path.
func endpointExpression(path string, params []Param) string {
	if len(params) == 0 {
		return fmt.Sprintf("%q", path)
	}

	format := pathParamPattern.ReplaceAllString(path, "%s")
	args := make([]string, 0, len(params))
	for _, name := range pathParamPattern.FindAllStringSubmatch(path, -1) {
		args = append(args, safeParamName(lowerFirst(goIdentifier(name[1]))))
	}
	return fmt.Sprintf("fmt.Sprintf(%q, %s)", format, strings.Join(args, ", "))
}

// operationDoc renders a method's doc comment.
func operationDoc(op Operation) string {
	var b strings.Builder

	summary := op.Summary
	if summary == "" {
		summary = "calls " + op.Method + " " + op.Path + "."
	} else {
		summary = lowerFirst(summary)
		if !strings.HasSuffix(summary, ".") {
			summary += "."
		}
	}
	fmt.Fprintf(&b, "// %s %s\n", op.Name, summary)
	fmt.Fprintf(&b, "// URL: %s %s", op.Method, op.Path)

	if len(op.QueryParams) > 0 {
		names := make([]string, 0, len(op.QueryParams))
		for _, q := range op.QueryParams {
			names = append(names, q.Name)
		}
		fmt.Fprintf(&b, "\n// Optional query params: %s", strings.Join(names, ", "))
	}

	if op.Description != "" && op.Description != op.Summary {
		fmt.Fprintf(&b, "\n//\n%s", commentBlock("", firstSentence(op.Description)))
	}

	return b.String()
}

// Write renders a service to disk under root, gofmt-ing the output.
func (g *Generator) Write(root string, svc Service) error {
	dir := filepath.Join(root, svc.Package)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	files := map[string]string{
		"service.go": "service.go.tmpl",
		"models.go":  "models.go.tmpl",
		"enums.go":   "enums.go.tmpl",
		"unions.go":  "unions.go.tmpl",
	}
	for out, tmpl := range files {
		if out == "models.go" && len(svc.Models) == 0 {
			continue
		}
		if out == "enums.go" && len(svc.Enums) == 0 {
			continue
		}
		if out == "unions.go" && len(svc.Unions) == 0 {
			continue
		}

		var buf bytes.Buffer
		if err := g.templates.ExecuteTemplate(&buf, tmpl, svc); err != nil {
			return fmt.Errorf("rendering %s for %s: %w", tmpl, svc.Package, err)
		}

		formatted, err := format.Source(buf.Bytes())
		if err != nil {
			// Keep the unformatted output so the failure can be inspected.
			_ = os.WriteFile(filepath.Join(dir, out+".broken"), buf.Bytes(), 0o600)
			return fmt.Errorf("formatting %s for %s: %w", out, svc.Package, err)
		}

		if err := os.WriteFile(filepath.Join(dir, out), formatted, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", out, err)
		}
	}
	return nil
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	// Preserve all-caps identifiers such as "ID".
	if strings.ToUpper(s) == s && len(s) > 1 {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// firstSentence trims a description down to its first sentence, since spec
// descriptions frequently run to several paragraphs of prose.
func firstSentence(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	s = strings.Join(strings.Fields(s), " ")
	if idx := strings.Index(s, ". "); idx > 0 {
		return s[:idx+1]
	}
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// commentBlock wraps text as a Go comment, prefixed by lead on the first line.
func commentBlock(lead, text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "//"
	}

	words := strings.Fields(lead + text)
	var (
		b    strings.Builder
		line strings.Builder
	)
	for _, w := range words {
		if line.Len() > 0 && line.Len()+len(w)+1 > 76 {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("// " + line.String())
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteString(" ")
		}
		line.WriteString(w)
	}
	if line.Len() > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("// " + line.String())
	}
	return b.String()
}

// resolveNameCollisions renames any model whose name clashes with the service
// type, since both land in the same package.
//
// The service name comes from the tag and the model names from the
// specification's component names, so "Account Groups" produces a service
// AccountGroups alongside a schema Administrative_API_AccountGroups that
// reduces to the same identifier. The model is renamed rather than the service,
// following the ResourceX convention go-sdk-jamfpro-v2 uses for its own models.
func resolveNameCollisions(serviceName string, models []Model, operations *[]Operation) []Model {
	renamed := map[string]string{}
	taken := map[string]bool{serviceName: true}
	for _, m := range models {
		if m.Name != serviceName {
			taken[m.Name] = true
		}
	}

	for i := range models {
		model := &models[i]
		if model.Name != serviceName {
			continue
		}
		candidate := "Resource" + model.Name
		for n := 2; taken[candidate]; n++ {
			candidate = fmt.Sprintf("Resource%s%d", model.Name, n)
		}
		taken[candidate] = true
		renamed[model.Name] = candidate
		model.Name = candidate
	}

	if len(renamed) == 0 {
		return models
	}

	// Rewrite references to the renamed types, both in other model bodies and
	// in the operation signatures.
	for old, replacement := range renamed {
		for i := range models {
			model := &models[i]
			model.Definition = replaceIdentifier(model.Definition, old, replacement)
		}
		for i := range *operations {
			op := &(*operations)[i]
			op.ResultType = replaceIdentifier(op.ResultType, old, replacement)
			op.BodyType = replaceIdentifier(op.BodyType, old, replacement)
			op.ReturnSig = replaceIdentifier(op.ReturnSig, old, replacement)
			op.ParamSig = replaceIdentifier(op.ParamSig, old, replacement)
		}
	}
	return models
}

// replaceIdentifier substitutes whole-word occurrences of old with replacement.
func replaceIdentifier(in, old, replacement string) string {
	if in == "" || !strings.Contains(in, old) {
		return in
	}
	pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(old) + `\b`)
	return pattern.ReplaceAllString(in, replacement)
}

// RootClient is the data the root client template renders from.
type RootClient struct {
	SourcePath  string
	SpecVersion string
	Services    []RootService
}

// RootService is one service as referenced from the root client.
type RootService struct {
	Package        string
	Name           string
	Field          string
	Tag            string
	OperationCount int
}

// BuildRootClient assembles the root client from the generated services.
func BuildRootClient(spec *Spec, services []Service) RootClient {
	root := RootClient{
		SourcePath:  filepath.Base(spec.Source),
		SpecVersion: spec.Version,
	}

	// Field names must be unique across all services. The service type name is
	// derived from the tag and is already unique, so it doubles as the field.
	for _, svc := range services {
		root.Services = append(root.Services, RootService{
			Package:        svc.Package,
			Name:           svc.Name,
			Field:          svc.Name,
			Tag:            svc.Tag,
			OperationCount: len(svc.Operations),
		})
	}
	return root
}

// WriteRootClient renders the root client to path.
func (g *Generator) WriteRootClient(path string, root RootClient) error {
	var buf bytes.Buffer
	if err := g.templates.ExecuteTemplate(&buf, "rootclient.go.tmpl", root); err != nil {
		return fmt.Errorf("rendering root client: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		_ = os.WriteFile(path+".broken", buf.Bytes(), 0o600)
		return fmt.Errorf("formatting root client: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, formatted, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
