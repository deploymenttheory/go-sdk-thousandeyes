// Command GetOpenAPISpecs downloads the ThousandEyes OpenAPI specification and
// stores it as a new snapshot when it differs from the last one.
//
// The specification is the source of truth this SDK is generated against, so it
// is version-controlled alongside the code. Snapshots are immutable: each run
// that finds a change writes a new directory rather than editing an old one.
//
//	go run ./scripts/openapi/GetOpenAPISpecs -dry-run   # report, write nothing
//	go run ./scripts/openapi/GetOpenAPISpecs            # snapshot if changed
//
// In CI the results are surfaced through GITHUB_OUTPUT so the workflow can decide
// whether to open a pull request. Exit status is zero whether or not anything
// changed; a non-zero status means the fetch or comparison itself failed.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/deploymenttheory/go-sdk-thousandeyes/internal/openapi"
)

const (
	defaultOutputDir = "openapi-specs"
	defaultTimeout   = 2 * time.Minute
)

type options struct {
	outputDir string
	baseURL   string
	specPath  string
	bodyPath  string
	dryRun    bool
	timeout   time.Duration
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run() error {
	opts := parseFlags()

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	client := &http.Client{Timeout: opts.timeout}

	// The catalogue resolves the specification's location rather than hardcoding
	// it, and doubles as a signal that a new API surface has appeared upstream.
	catalogue, err := openapi.FetchCatalogue(ctx, client, opts.baseURL)
	if err != nil {
		return err
	}
	log.Printf("catalogue: %d specifications listed", len(catalogue))

	if _, ok := openapi.Find(catalogue, opts.specPath); !ok {
		return fmt.Errorf("%s is not listed in the catalogue; it may have been renamed", opts.specPath)
	}

	current, err := openapi.FetchSpec(ctx, client, opts.baseURL, opts.specPath)
	if err != nil {
		return err
	}
	log.Printf("fetched %s: version %s, %d paths, %d operations",
		opts.specPath, current.Version, current.PathCount(), len(current.Operations))

	previous, previousCatalogue, err := loadPrevious(opts)
	if err != nil {
		return err
	}

	diff := openapi.Compare(previous, current, previousCatalogue, openapi.Titles(catalogue))
	log.Printf("comparison: %s", diff.Summary())

	report := report{
		diff:           diff,
		sourceURL:      openapi.CatalogueEntry{Content: opts.specPath}.URL(opts.baseURL),
		pathCount:      current.PathCount(),
		operationCount: len(current.Operations),
		catalogueCount: len(catalogue),
	}

	if !diff.Changed() {
		log.Printf("no change since the last snapshot; nothing to do")
		return emit(opts, report, "", false)
	}

	if opts.dryRun {
		log.Printf("dry run: would write a snapshot for %s", current.Version)
		return emit(opts, report, "", true)
	}

	now := time.Now().UTC()
	meta := openapi.Metadata{
		Version:             current.Version,
		SHA256:              current.SHA256,
		SourcePath:          opts.specPath,
		SourceURL:           openapi.CatalogueEntry{Content: opts.specPath}.URL(opts.baseURL),
		FetchedAt:           now,
		PathCount:           current.PathCount(),
		OperationCount:      len(current.Operations),
		CatalogueEntryCount: len(catalogue),
		CatalogueEntries:    openapi.Titles(catalogue),
	}

	dir, err := openapi.Write(opts.outputDir, current, meta, now)
	if err != nil {
		return err
	}
	log.Printf("wrote snapshot %s", dir)

	return emit(opts, report, dir, true)
}

// report bundles everything needed to describe a run.
type report struct {
	diff           openapi.Diff
	sourceURL      string
	pathCount      int
	operationCount int
	catalogueCount int
}

func parseFlags() options {
	var opts options

	flag.StringVar(&opts.outputDir, "output-dir", defaultOutputDir, "Directory snapshots are written to")
	flag.StringVar(&opts.baseURL, "base-url", openapi.DefaultBaseURL, "Base URL the specification catalogue is served from")
	flag.StringVar(&opts.specPath, "spec", openapi.UnifiedSpecPath, "Catalogue path of the specification to collect")
	flag.StringVar(&opts.bodyPath, "body-path", "", "Write the pull request body here (default <output-dir>/.pr-body.md)")
	flag.BoolVar(&opts.dryRun, "dry-run", false, "Fetch and compare without writing a snapshot")
	flag.DurationVar(&opts.timeout, "timeout", defaultTimeout, "Overall timeout")

	flag.Parse()

	if opts.bodyPath == "" {
		opts.bodyPath = filepath.Join(opts.outputDir, ".pr-body.md")
	}
	return opts
}

// loadPrevious returns the most recent snapshot's specification and the
// catalogue recorded with it. Both are nil when this is the first run.
func loadPrevious(opts options) (*openapi.Spec, []string, error) {
	latest, err := openapi.LatestSnapshot(opts.outputDir)
	if errors.Is(err, openapi.ErrNoSnapshot) {
		log.Printf("no previous snapshot found; this will be the first")
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	log.Printf("previous snapshot: %s (version %s)", filepath.Base(latest.Dir), latest.Version)

	spec, err := latest.Load(opts.specPath)
	if err != nil {
		return nil, nil, err
	}

	// The catalogue recorded alongside the previous snapshot is optional: older
	// snapshots may predate it, and its absence only means catalogue changes
	// cannot be reported for this run.
	var meta openapi.Metadata
	raw, err := os.ReadFile(filepath.Join(latest.Dir, openapi.MetadataFileName))
	if err == nil {
		if err := json.Unmarshal(raw, &meta); err != nil {
			log.Printf("warning: previous metadata unreadable, catalogue changes will not be reported: %v", err)
		}
	}

	return spec, meta.CatalogueEntries, nil
}

// emit writes the pull request body and the workflow outputs.
func emit(opts options, r report, snapshotDir string, changed bool) error {
	if changed {
		body := r.diff.Body(r.sourceURL, r.pathCount, r.operationCount, r.catalogueCount)
		if err := os.MkdirAll(filepath.Dir(opts.bodyPath), 0o755); err != nil {
			return fmt.Errorf("creating body directory: %w", err)
		}
		if err := os.WriteFile(opts.bodyPath, []byte(body), 0o644); err != nil {
			return fmt.Errorf("writing pull request body: %w", err)
		}
		log.Printf("wrote pull request body to %s", opts.bodyPath)
	}

	// Without a snapshot directory there is nothing to name a branch after, which
	// is the case on a dry run and when nothing changed.
	branch := ""
	if snapshotDir != "" {
		branch = "openapi-specs-" + filepath.Base(snapshotDir)
	}

	outputs := map[string]string{
		"changed":      fmt.Sprintf("%t", changed),
		"version":      r.diff.CurrentVersion,
		"summary":      r.diff.Summary(),
		"title":        r.diff.Title(),
		"snapshot_dir": snapshotDir,
		"body_path":    opts.bodyPath,
		"branch":       branch,
	}

	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		for k, v := range outputs {
			log.Printf("output %s=%s", k, v)
		}
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("opening GITHUB_OUTPUT: %w", err)
	}
	defer func() { _ = f.Close() }()

	for k, v := range outputs {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, v); err != nil {
			return fmt.Errorf("writing GITHUB_OUTPUT: %w", err)
		}
	}
	return nil
}
