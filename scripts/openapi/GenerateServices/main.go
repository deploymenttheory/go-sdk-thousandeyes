// Command GenerateServices renders the ThousandEyes API service packages from a
// stored OpenAPI snapshot.
//
// One package is emitted per specification tag, laid out like
// go-sdk-jamfpro-v2's jamf_pro_api: a Service struct holding a client.Client,
// a New<Service> constructor, and one method per operation returning
// (*Result, *resty.Response, error).
//
//	go run ./scripts/openapi/GenerateServices
//	go run ./scripts/openapi/GenerateServices -only "BGP Tests"
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deploymenttheory/go-sdk-thousandeyes/internal/codegen"
	"github.com/deploymenttheory/go-sdk-thousandeyes/internal/openapi"
)

const (
	defaultSpecRoot = "openapi-specs"
	defaultOutput   = "thousandeyes/thousandeyes_api"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run() error {
	var (
		specPath       = flag.String("spec", "", "Path to the OpenAPI document (default: newest snapshot)")
		specRoot       = flag.String("spec-root", defaultSpecRoot, "Directory holding the snapshots")
		outputDir      = flag.String("output", defaultOutput, "Directory to write service packages to")
		only           = flag.String("only", "", "Generate a single tag, for inspecting output")
		clean          = flag.Bool("clean", true, "Remove previously generated packages first")
		list           = flag.Bool("list", false, "List the tags that would be generated and exit")
		rootClientPath = flag.String("root-client", "thousandeyes/thousandeyes.go", "Path of the generated root client")
	)
	flag.Parse()

	path := *specPath
	if path == "" {
		resolved, err := newestSpec(*specRoot)
		if err != nil {
			return err
		}
		path = resolved
	}
	log.Printf("specification: %s", path)

	spec, err := codegen.LoadSpec(path)
	if err != nil {
		return err
	}
	log.Printf("API version %s: %d paths", spec.Version, len(spec.Paths))

	generator, err := codegen.NewGenerator(spec)
	if err != nil {
		return err
	}

	services := generator.Build()
	if *only != "" {
		services = filterTag(services, *only)
		if len(services) == 0 {
			return fmt.Errorf("no tag matching %q", *only)
		}
	}

	if *list {
		for _, svc := range services {
			fmt.Printf("%-44s %-34s %d operations\n", svc.Tag, svc.Package, len(svc.Operations))
		}
		return nil
	}

	if *clean && *only == "" {
		if err := os.RemoveAll(*outputDir); err != nil {
			return fmt.Errorf("cleaning %s: %w", *outputDir, err)
		}
	}

	var operations, models int
	for _, svc := range services {
		if err := generator.Write(*outputDir, svc); err != nil {
			return err
		}
		operations += len(svc.Operations)
		models += len(svc.Models)
	}

	// The root client wires every service onto one transport, so it is only
	// coherent when the full set has been generated.
	if *only == "" {
		root := codegen.BuildRootClient(spec, services)
		if err := generator.WriteRootClient(*rootClientPath, root); err != nil {
			return err
		}
		log.Printf("wrote root client %s", *rootClientPath)
	}

	log.Printf("generated %d packages, %d operations, %d models into %s",
		len(services), operations, models, *outputDir)
	return nil
}

// newestSpec returns the api.yaml of the most recent snapshot.
func newestSpec(root string) (string, error) {
	latest, err := openapi.LatestSnapshot(root)
	if err != nil {
		return "", fmt.Errorf("locating a snapshot in %s: %w", root, err)
	}
	return filepath.Join(latest.Dir, openapi.SpecFileName), nil
}

func filterTag(services []codegen.Service, want string) []codegen.Service {
	var out []codegen.Service
	for _, svc := range services {
		if strings.EqualFold(svc.Tag, want) || strings.EqualFold(svc.Package, want) {
			out = append(out, svc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out
}
