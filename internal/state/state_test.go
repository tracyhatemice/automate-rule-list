package state

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadAbsent(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	st, found, err := s.Load("job")
	if err != nil || found || st != nil {
		t.Fatalf("Load absent = %v %v %v", st, found, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	in := &State{Hash: "abc", UpdatedAt: "2026-09-07T00:00:00Z", Outputs: map[string]OutputState{
		"dist/a.txt": {Hash: "h1", UploadedTo: []string{"s3://b/k", "s3://c/k"}},
	}}
	if err := s.Save("job", in); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.Load("job")
	if err != nil || !found {
		t.Fatalf("Load = %v %v", found, err)
	}
	if got.Hash != in.Hash || got.UpdatedAt != in.UpdatedAt || got.Outputs["dist/a.txt"].Hash != "h1" || !slices.Equal(got.Outputs["dist/a.txt"].UploadedTo, []string{"s3://b/k", "s3://c/k"}) {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "job", "state.json")); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
}

func TestLoadLegacyUploadedToString(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(s.Dir, "job"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"hash":"h","updated_at":"t","outputs":{"a":{"hash":"x","uploaded_to":"s3://b/k"},"b":{"hash":"y"}}}`
	if err := os.WriteFile(filepath.Join(s.Dir, "job", "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	st, found, err := s.Load("job")
	if err != nil || !found {
		t.Fatalf("Load = %v %v", found, err)
	}
	if !slices.Equal(st.Outputs["a"].UploadedTo, []string{"s3://b/k"}) || len(st.Outputs["b"].UploadedTo) != 0 {
		t.Fatalf("legacy parse: %+v", st.Outputs)
	}
}

func TestLoadCorrupt(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(s.Dir, "job"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "job", "state.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("job"); err == nil {
		t.Fatal("expected error for corrupt state")
	}
}

func TestIntermediate(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.WriteIntermediate("job", []string{"a.com", "b.com"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(s.IntermediatePath("job"))
	if err != nil || string(b) != "a.com\nb.com\n" {
		t.Fatalf("intermediate = %q %v", b, err)
	}
	if err := s.WriteIntermediate("job", nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(s.IntermediatePath("job"))
	if string(b) != "" {
		t.Fatalf("empty intermediate = %q", b)
	}
}

func TestHash(t *testing.T) {
	a := HashLines([]string{"a", "b"})
	if a != HashLines([]string{"a", "b"}) {
		t.Fatal("hash not stable")
	}
	if a == HashLines([]string{"b", "a"}) {
		t.Fatal("hash ignores order")
	}
	if a != HashBytes([]byte("a\nb\n")) {
		t.Fatal("HashLines must equal HashBytes of the joined text")
	}
	if len(a) != 64 {
		t.Fatalf("hash length = %d", len(a))
	}
}
