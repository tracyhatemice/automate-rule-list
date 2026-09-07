// Package state persists, per job, the intermediate lines of the last run
// and a small JSON record used for change detection and output tracking.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OutputState records what was last written and uploaded for one output.
type OutputState struct {
	Hash       string     `json:"hash"`
	UploadedTo UploadList `json:"uploaded_to,omitempty"` // URIs that hold the content with this hash
}

// UploadList is a list of upload URIs that also reads the single-string
// form written by earlier versions.
type UploadList []string

// UnmarshalJSON accepts a JSON string or a list of strings.
func (u *UploadList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		if one == "" {
			*u = nil
		} else {
			*u = UploadList{one}
		}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*u = list
	return nil
}

// Has reports whether uri is in the list.
func (u UploadList) Has(uri string) bool {
	for _, v := range u {
		if v == uri {
			return true
		}
	}
	return false
}

// State is the per-job record.
type State struct {
	Hash      string                 `json:"hash"`       // hash of the intermediate lines
	UpdatedAt string                 `json:"updated_at"` // timestamp of the last content change
	Outputs   map[string]OutputState `json:"outputs"`    // keyed by output path
}

// Store keeps one directory per job under Dir.
type Store struct {
	Dir string
}

func (s Store) jobDir(job string) string { return filepath.Join(s.Dir, job) }

func (s Store) statePath(job string) string { return filepath.Join(s.jobDir(job), "state.json") }

// IntermediatePath returns where the intermediate lines of job are kept.
func (s Store) IntermediatePath(job string) string {
	return filepath.Join(s.jobDir(job), "intermediate.txt")
}

// Load reads the state of job. found is false when no state exists yet.
func (s Store) Load(job string) (st *State, found bool, err error) {
	b, err := os.ReadFile(s.statePath(job))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	st = &State{}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, false, fmt.Errorf("state %s: %w", s.statePath(job), err)
	}
	if st.Outputs == nil {
		st.Outputs = map[string]OutputState{}
	}
	return st, true, nil
}

// Save writes the state of job atomically.
func (s Store) Save(job string, st *State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(s.statePath(job), append(b, '\n'))
}

// WriteIntermediate stores lines, one per line, atomically.
func (s Store) WriteIntermediate(job string, lines []string) error {
	return WriteFileAtomic(s.IntermediatePath(job), []byte(Join(lines)))
}

// Join renders lines as newline-terminated text.
func Join(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// HashLines hashes the newline-joined lines.
func HashLines(lines []string) string { return HashBytes([]byte(Join(lines))) }

// HashBytes returns the hex SHA-256 of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// WriteFileAtomic writes data to path via a temporary file and rename,
// creating parent directories as needed.
func WriteFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
