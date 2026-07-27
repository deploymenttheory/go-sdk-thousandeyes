package openapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// ErrNoSnapshot is returned by LatestSnapshot when there is nothing stored yet,
// which is the normal state on a first run rather than a failure.
var ErrNoSnapshot = errors.New("no snapshot found")

// SpecFileName is the name every snapshot stores its document under. The
// directory carries the version, so the file itself does not need to.
const SpecFileName = "api.yaml"

// MetadataFileName describes a snapshot without needing to re-parse the spec.
const MetadataFileName = "metadata.json"

// snapshotDirPattern matches the directory convention shared with
// go-sdk-jamfpro-v2: the upstream version, then a millisecond timestamp.
var snapshotDirPattern = regexp.MustCompile(`^(.+)-t(\d+)$`)

// Snapshot is a stored copy of a specification at a point in time.
type Snapshot struct {
	// Dir is the absolute path of the snapshot directory.
	Dir string
	// Version is the upstream version the snapshot was taken at.
	Version string
	// Timestamp is when it was taken.
	Timestamp time.Time
	// SHA256 is the checksum of the stored document.
	SHA256 string
}

// Metadata is written alongside each stored specification so a snapshot is
// self-describing.
type Metadata struct {
	Version             string    `json:"version"`
	SHA256              string    `json:"sha256"`
	SourcePath          string    `json:"sourcePath"`
	SourceURL           string    `json:"sourceUrl"`
	FetchedAt           time.Time `json:"fetchedAt"`
	PathCount           int       `json:"pathCount"`
	OperationCount      int       `json:"operationCount"`
	CatalogueEntryCount int       `json:"catalogueEntryCount"`
	CatalogueEntries    []string  `json:"catalogueEntries"`
}

// SnapshotDirName builds the directory name for a version taken at a given time.
func SnapshotDirName(version string, at time.Time) string {
	if version == "" {
		version = "unknown"
	}
	return fmt.Sprintf("%s-t%d", version, at.UnixMilli())
}

// LatestSnapshot returns the most recent snapshot under root, or ErrNoSnapshot
// when there is none. Ordering is by the timestamp encoded in the directory name
// rather than by filesystem times, which are not preserved by a git checkout.
func LatestSnapshot(root string) (*Snapshot, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoSnapshot
		}
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}

	var found []Snapshot
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := snapshotDirPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		millis, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			continue
		}

		dir := filepath.Join(root, e.Name())
		sum, err := checksumFile(filepath.Join(dir, SpecFileName))
		if err != nil {
			// A directory without a readable spec is not a usable baseline;
			// skip it rather than failing the whole run.
			continue
		}

		found = append(found, Snapshot{
			Dir:       dir,
			Version:   m[1],
			Timestamp: time.UnixMilli(millis),
			SHA256:    sum,
		})
	}

	if len(found) == 0 {
		return nil, ErrNoSnapshot
	}

	sort.Slice(found, func(i, j int) bool {
		return found[i].Timestamp.Before(found[j].Timestamp)
	})
	latest := found[len(found)-1]
	return &latest, nil
}

// Load reads the specification stored in a snapshot.
func (s *Snapshot) Load(source string) (*Spec, error) {
	raw, err := os.ReadFile(filepath.Join(s.Dir, SpecFileName))
	if err != nil {
		return nil, fmt.Errorf("reading snapshot spec: %w", err)
	}
	return ParseSpec(source, raw)
}

// Write stores a specification as a new snapshot under root and returns the
// directory created. The document is written byte-for-byte as served.
func Write(root string, spec *Spec, meta Metadata, at time.Time) (string, error) {
	dir := filepath.Join(root, SnapshotDirName(spec.Version, at))

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	if err := os.WriteFile(filepath.Join(dir, SpecFileName), spec.Raw, 0o600); err != nil {
		return "", fmt.Errorf("writing spec: %w", err)
	}

	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding metadata: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := os.WriteFile(filepath.Join(dir, MetadataFileName), encoded, 0o600); err != nil {
		return "", fmt.Errorf("writing metadata: %w", err)
	}

	return dir, nil
}

func checksumFile(path string) (string, error) {
	// #nosec G304 -- path is built from a repo-local snapshot directory
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
