package openapi

import (
	"fmt"
	"sort"
	"strings"
)

// maxListed caps how many entries of a category are named individually in the
// pull request body. A bulk regeneration upstream can change hundreds of
// operations at once, and an unreadable PR body helps nobody.
const maxListed = 25

// Diff describes what changed between two versions of a specification.
type Diff struct {
	// PreviousVersion is empty when there is no earlier snapshot.
	PreviousVersion string
	CurrentVersion  string

	PreviousSHA256 string
	CurrentSHA256  string

	// AddedOperations, RemovedOperations and ModifiedOperations hold
	// "METHOD /path" identifiers, sorted.
	AddedOperations    []string
	RemovedOperations  []string
	ModifiedOperations []string

	// AddedCatalogueEntries and RemovedCatalogueEntries track specifications
	// appearing in or disappearing from the upstream catalogue. Only the unified
	// document is stored, but a new entry signals a new API surface.
	AddedCatalogueEntries   []string
	RemovedCatalogueEntries []string

	// FirstSnapshot is true when there was no previous snapshot to compare to.
	FirstSnapshot bool
}

// Changed reports whether anything of substance differs.
func (d Diff) Changed() bool {
	return d.FirstSnapshot ||
		d.PreviousSHA256 != d.CurrentSHA256 ||
		len(d.AddedCatalogueEntries) > 0 ||
		len(d.RemovedCatalogueEntries) > 0
}

// VersionChanged reports whether the upstream version string moved.
func (d Diff) VersionChanged() bool {
	return d.PreviousVersion != "" && d.PreviousVersion != d.CurrentVersion
}

// Compare produces a Diff between a previous specification and the current one.
// A nil previous means this is the first snapshot.
func Compare(previous, current *Spec, previousCatalogue, currentCatalogue []string) Diff {
	d := Diff{
		CurrentVersion:          current.Version,
		CurrentSHA256:           current.SHA256,
		AddedCatalogueEntries:   missingFrom(currentCatalogue, previousCatalogue),
		RemovedCatalogueEntries: missingFrom(previousCatalogue, currentCatalogue),
	}

	// With no previous catalogue recorded there is nothing to compare against,
	// so every entry would look new. Report none rather than all.
	if len(previousCatalogue) == 0 {
		d.AddedCatalogueEntries = nil
		d.RemovedCatalogueEntries = nil
	}

	if previous == nil {
		d.FirstSnapshot = true
		d.AddedOperations = current.OperationKeys()
		return d
	}

	d.PreviousVersion = previous.Version
	d.PreviousSHA256 = previous.SHA256

	for key, sum := range current.Operations {
		prev, existed := previous.Operations[key]
		switch {
		case !existed:
			d.AddedOperations = append(d.AddedOperations, key)
		case prev != sum:
			d.ModifiedOperations = append(d.ModifiedOperations, key)
		}
	}
	for key := range previous.Operations {
		if _, still := current.Operations[key]; !still {
			d.RemovedOperations = append(d.RemovedOperations, key)
		}
	}

	sort.Strings(d.AddedOperations)
	sort.Strings(d.RemovedOperations)
	sort.Strings(d.ModifiedOperations)

	return d
}

// Title is the pull request and commit subject.
func (d Diff) Title() string {
	if d.FirstSnapshot {
		return fmt.Sprintf("openapi: add ThousandEyes API specs for %s", d.CurrentVersion)
	}
	if d.VersionChanged() {
		return fmt.Sprintf("openapi: update ThousandEyes API specs to %s", d.CurrentVersion)
	}
	return fmt.Sprintf("openapi: refresh ThousandEyes API specs (%s)", d.CurrentVersion)
}

// Body renders the pull request description.
func (d Diff) Body(sourceURL string, pathCount, operationCount, catalogueCount int) string {
	var b strings.Builder

	b.WriteString("## ThousandEyes OpenAPI specification update\n\n")

	switch {
	case d.FirstSnapshot:
		fmt.Fprintf(&b, "First snapshot, at version `%s`.\n\n", d.CurrentVersion)
	case d.VersionChanged():
		fmt.Fprintf(&b, "Version `%s` → `%s`.\n\n", d.PreviousVersion, d.CurrentVersion)
	default:
		fmt.Fprintf(
			&b,
			"Version `%s` unchanged; the document was republished with different content.\n\n",
			d.CurrentVersion,
		)
	}

	b.WriteString("| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Source | %s |\n", sourceURL)
	fmt.Fprintf(&b, "| SHA-256 | `%s` |\n", d.CurrentSHA256)
	if d.PreviousSHA256 != "" {
		fmt.Fprintf(&b, "| Previous SHA-256 | `%s` |\n", d.PreviousSHA256)
	}
	fmt.Fprintf(&b, "| Paths | %d |\n", pathCount)
	fmt.Fprintf(&b, "| Operations | %d |\n", operationCount)
	fmt.Fprintf(&b, "| Catalogue entries | %d |\n", catalogueCount)
	b.WriteString("\n")

	if d.FirstSnapshot {
		b.WriteString(
			"No previous snapshot to compare against, so the full surface is recorded as added.\n",
		)
		return b.String()
	}

	section(&b, "Operations added", d.AddedOperations)
	section(&b, "Operations removed", d.RemovedOperations)
	section(&b, "Operations modified", d.ModifiedOperations)
	section(&b, "Catalogue entries added", d.AddedCatalogueEntries)
	section(&b, "Catalogue entries removed", d.RemovedCatalogueEntries)

	if len(d.AddedOperations) == 0 && len(d.RemovedOperations) == 0 &&
		len(d.ModifiedOperations) == 0 && len(d.AddedCatalogueEntries) == 0 &&
		len(d.RemovedCatalogueEntries) == 0 {
		b.WriteString("No operation-level changes detected. The document differs in " +
			"descriptions, examples or formatting only — review the file diff for detail.\n")
	}

	return b.String()
}

// Summary is a one-line description for logs and workflow output.
func (d Diff) Summary() string {
	if d.FirstSnapshot {
		return fmt.Sprintf(
			"first snapshot at %s: %d operations",
			d.CurrentVersion,
			len(d.AddedOperations),
		)
	}
	return fmt.Sprintf(
		"%s: +%d/-%d/~%d operations",
		d.CurrentVersion,
		len(d.AddedOperations),
		len(d.RemovedOperations),
		len(d.ModifiedOperations),
	)
}

func section(b *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}

	fmt.Fprintf(b, "### %s (%d)\n\n", heading, len(items))
	shown := items
	if len(shown) > maxListed {
		shown = shown[:maxListed]
	}
	for _, item := range shown {
		fmt.Fprintf(b, "- `%s`\n", item)
	}
	if len(items) > len(shown) {
		fmt.Fprintf(b, "- …and %d more\n", len(items)-len(shown))
	}
	b.WriteString("\n")
}

// missingFrom returns the entries of a that do not appear in b.
func missingFrom(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, v := range b {
		inB[v] = true
	}

	var out []string
	for _, v := range a {
		if !inB[v] {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
