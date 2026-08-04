// Package codegen turns the ThousandEyes OpenAPI specification into Go service
// packages, following the layout of go-sdk-jamfpro-v2's jamf_pro_api: one
// package per API area, a Service struct holding a client.Client, and methods
// returning (*Result, *resty.Response, error).
package codegen

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec is the parsed OpenAPI document, kept generic rather than bound to a
// typed OpenAPI model so that an unexpected upstream shape degrades to a
// skipped operation instead of failing the whole generation run.
type Spec struct {
	Version string
	Paths   map[string]map[string]any
	Schemas map[string]any
	// Components holds every component section — parameters, responses,
	// requestBodies and the rest — because the specification $refs into all of
	// them, not only schemas.
	Components map[string]map[string]any
	Source     string
}

// LoadSpec reads and parses an OpenAPI document from disk.
func LoadSpec(path string) (*Spec, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path is an operator-supplied spec file
	if err != nil {
		return nil, fmt.Errorf("reading spec: %w", err)
	}

	var doc struct {
		Info struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
		Paths      map[string]map[string]any `yaml:"paths"`
		Components map[string]map[string]any `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decoding spec: %w", err)
	}

	return &Spec{
		Version:    doc.Info.Version,
		Paths:      doc.Paths,
		Schemas:    doc.Components["schemas"],
		Components: doc.Components,
		Source:     path,
	}, nil
}

// httpMethods are the path-item keys that denote an operation.
var httpMethods = map[string]string{
	"get": "Get", "post": "Post", "put": "Put",
	"patch": "Patch", "delete": "Delete",
}

// RawOperation is one operation as it appears in the document.
type RawOperation struct {
	Path        string
	Method      string
	OperationID string
	Summary     string
	Description string
	Tag         string
	Op          map[string]any

	// PathItemParams are the parameters declared on the path item rather than on
	// the operation. OpenAPI shares them across every method of the path, and the
	// specification uses that form: /endpoint/labels/{id} declares its id there
	// and nowhere else, which is how three generated methods came to send a
	// literal "{id}" to the API.
	PathItemParams []any
}

// Operations returns every operation in the document, sorted for deterministic
// output. Operations without a tag or an operationId are skipped: the tag
// decides the package and the operationId the method name, so neither can be
// invented safely.
func (s *Spec) Operations() []RawOperation {
	var ops []RawOperation

	for path, item := range s.Paths {
		shared, _ := item["parameters"].([]any)

		for method, raw := range item {
			if _, ok := httpMethods[strings.ToLower(method)]; !ok {
				continue
			}
			op, ok := raw.(map[string]any)
			if !ok {
				continue
			}

			id, _ := op["operationId"].(string)
			if id == "" {
				continue
			}

			tags, _ := op["tags"].([]any)
			if len(tags) == 0 {
				continue
			}
			tag, _ := tags[0].(string)
			if tag == "" {
				continue
			}

			summary, _ := op["summary"].(string)
			description, _ := op["description"].(string)

			ops = append(ops, RawOperation{
				Path:           path,
				Method:         strings.ToUpper(method),
				OperationID:    id,
				Summary:        summary,
				Description:    description,
				Tag:            tag,
				Op:             op,
				PathItemParams: shared,
			})
		}
	}

	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Tag != ops[j].Tag {
			return ops[i].Tag < ops[j].Tag
		}
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})

	return ops
}

// Resolve follows a local $ref such as "#/components/schemas/Tests_API_Test" or
// "#/components/parameters/Tests_API_TestIdPath".
//
// Every component section is resolvable, not only schemas: operations reference
// shared parameters, responses and request bodies by name, and treating those
// as unresolvable silently drops path parameters from generated signatures.
//
// Anything else — a remote or unrecognised reference — yields false rather than
// an error, and the caller falls back to an untyped value.
func (s *Spec) Resolve(ref string) (map[string]any, string, bool) {
	const prefix = "#/components/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, "", false
	}

	rest := strings.TrimPrefix(ref, prefix)
	section, name, ok := strings.Cut(rest, "/")
	if !ok || name == "" {
		return nil, "", false
	}

	entries, ok := s.Components[section]
	if !ok {
		return nil, name, false
	}
	resolved, ok := entries[name].(map[string]any)
	if !ok {
		return nil, name, false
	}
	return resolved, name, true
}
