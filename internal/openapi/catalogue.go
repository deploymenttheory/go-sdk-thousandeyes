// Package openapi collects the ThousandEyes OpenAPI specifications that this SDK
// is generated against.
//
// Cisco publishes the v7 API documentation through DevNet's pubhub CDN. The
// documentation site is a single-page app, but it is driven by a machine-readable
// catalogue at config.json which lists every specification and the path it is
// served from. Resolving specs through that catalogue avoids scraping the SPA and
// avoids hardcoding the UUID-bearing URLs the documentation links to, which are an
// alternate form of the same files and may rotate.
package openapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrCatalogueEmpty is returned when the catalogue parses but lists no
// specifications, which means its format has changed and the walk below no
// longer finds them.
var ErrCatalogueEmpty = errors.New("no .yaml entries found; the catalogue format may have changed")

// DefaultBaseURL is the pubhub location the v7 documentation is published under.
// Every catalogue entry's Content is relative to this.
const DefaultBaseURL = "https://pubhub.devnetcloud.com/media/000-v7-apis/docs"

// UnifiedSpecPath is the catalogue entry for the unified specification: a single
// document covering the whole v7 surface. The per-domain specs are subsets of it,
// and they version independently of one another, so the unified document is the
// one this SDK is generated from.
const UnifiedSpecPath = "reference/unified-oas/api.yaml"

// CatalogueEntry is a single specification listed in the catalogue.
type CatalogueEntry struct {
	// Title is the human-readable name, e.g. "Endpoint Tests API".
	Title string
	// Content is the path relative to the base URL, e.g. "reference/tests/tests.yaml".
	Content string
	// Type is the catalogue's own type marker; OpenAPI 3 documents are "oas3".
	Type string
}

// URL returns the absolute location of the entry under base.
func (e CatalogueEntry) URL(base string) string {
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(e.Content, "/")
}

// FetchCatalogue retrieves and parses the specification catalogue.
//
// Only the unified specification is stored by this pipeline, but the full
// catalogue is still read on every run so that a new API surface appearing
// upstream can be reported rather than passing unnoticed.
func FetchCatalogue(
	ctx context.Context,
	client *http.Client,
	base string,
) ([]CatalogueEntry, error) {
	url := strings.TrimSuffix(base, "/") + "/config.json"

	body, err := get(ctx, client, url)
	if err != nil {
		return nil, fmt.Errorf("fetching catalogue: %w", err)
	}

	entries, err := ParseCatalogue(body)
	if err != nil {
		return nil, fmt.Errorf("parsing catalogue from %s: %w", url, err)
	}
	return entries, nil
}

// ParseCatalogue extracts the specification entries from a raw config.json.
//
// The catalogue is a navigation tree, so entries are found by walking it rather
// than by indexing a known position: specs currently sit about four levels deep
// and that nesting is presentational, not contractual.
func ParseCatalogue(raw []byte) ([]CatalogueEntry, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("decoding json: %w", err)
	}

	var entries []CatalogueEntry
	seen := make(map[string]bool)

	var walk func(node any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if content, ok := n["content"].(string); ok && strings.HasSuffix(content, ".yaml") {
				if !seen[content] {
					seen[content] = true
					title, _ := n["title"].(string)
					typ, _ := n["type"].(string)
					entries = append(entries, CatalogueEntry{
						Title:   title,
						Content: content,
						Type:    typ,
					})
				}
			}
			for _, v := range n {
				walk(v)
			}
		case []any:
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(root)

	if len(entries) == 0 {
		return nil, ErrCatalogueEmpty
	}
	return entries, nil
}

// Find returns the entry whose Content matches path.
func Find(entries []CatalogueEntry, path string) (CatalogueEntry, bool) {
	for _, e := range entries {
		if e.Content == path {
			return e, true
		}
	}
	return CatalogueEntry{}, false
}

// Titles returns the entry titles, for reporting catalogue changes between runs.
func Titles(entries []CatalogueEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Content)
	}
	return out
}
