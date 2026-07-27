package openapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnexpectedStatus is returned when the documentation host answers with
// anything other than a 2xx.
var ErrUnexpectedStatus = errors.New("unexpected status")

// Spec is a downloaded OpenAPI document together with the facts the pipeline
// needs to decide whether it has changed and to describe how.
type Spec struct {
	// Source is the path within the catalogue, e.g. "reference/unified-oas/api.yaml".
	Source string
	// Version is info.version from the document, e.g. "7.0.97".
	Version string
	// SHA256 is the checksum of the raw bytes, and is what change detection turns on.
	SHA256 string
	// Raw is the document exactly as served. It is written to disk unmodified so a
	// snapshot is always a faithful copy of what upstream published.
	Raw []byte
	// Operations maps "METHOD /path" to a checksum of that operation's definition,
	// which is what lets a diff report modified operations rather than only
	// added and removed ones.
	Operations map[string]string
}

// httpMethods are the OpenAPI path-item keys that denote an operation. Other keys
// (parameters, servers, summary, ...) describe the path itself.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// FetchSpec downloads and parses a single specification.
func FetchSpec(ctx context.Context, client *http.Client, base, path string) (*Spec, error) {
	url := strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")

	raw, err := get(ctx, client, url)
	if err != nil {
		return nil, fmt.Errorf("fetching spec %s: %w", path, err)
	}

	spec, err := ParseSpec(path, raw)
	if err != nil {
		return nil, fmt.Errorf("parsing spec %s: %w", path, err)
	}
	return spec, nil
}

// ParseSpec derives the pipeline's view of a specification from its raw bytes.
//
// The document is decoded into a generic structure rather than a typed OpenAPI
// model. The pipeline only needs the version and an operation inventory, and a
// generic decode means an unexpected upstream shape degrades to a coarser diff
// instead of failing the run outright.
func ParseSpec(source string, raw []byte) (*Spec, error) {
	sum := sha256.Sum256(raw)

	spec := &Spec{
		Source:     source,
		SHA256:     hex.EncodeToString(sum[:]),
		Raw:        raw,
		Operations: map[string]string{},
	}

	var doc struct {
		Info struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decoding yaml: %w", err)
	}

	spec.Version = doc.Info.Version

	for path, item := range doc.Paths {
		for method, node := range item {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			// Re-encode the operation so the checksum reflects its definition
			// rather than incidental formatting elsewhere in the document.
			encoded, err := yaml.Marshal(&node)
			if err != nil {
				return nil, fmt.Errorf("re-encoding %s %s: %w", method, path, err)
			}
			opSum := sha256.Sum256(encoded)
			key := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
			spec.Operations[key] = hex.EncodeToString(opSum[:])
		}
	}

	return spec, nil
}

// OperationKeys returns the operation identifiers in a stable order.
func (s *Spec) OperationKeys() []string {
	keys := make([]string, 0, len(s.Operations))
	for k := range s.Operations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// PathCount returns the number of distinct paths across all operations.
func (s *Spec) PathCount() int {
	paths := map[string]bool{}
	for key := range s.Operations {
		if _, path, ok := strings.Cut(key, " "); ok {
			paths[path] = true
		}
	}
	return len(paths)
}

// get performs a GET and returns the body, failing on any non-2xx status.
func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%w: %s for %s", ErrUnexpectedStatus, resp.Status, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body of %s: %w", url, err)
	}
	return body, nil
}
