package fetch

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newFetcher(t *testing.T) *Fetcher {
	t.Helper()
	return &Fetcher{
		Client:    &http.Client{Timeout: 5 * time.Second},
		Retries:   2,
		Backoff:   time.Millisecond,
		UserAgent: "rulegen-test/1",
		MaxBody:   1 << 20,
		BaseDir:   t.TempDir(),
	}
}

func TestFetchURL(t *testing.T) {
	var flaky atomic.Int32
	var gotUA atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA.Store(r.Header.Get("User-Agent"))
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("hello\n"))
		case "/flaky":
			if flaky.Add(1) < 3 {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte("recovered"))
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", 2<<20)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := newFetcher(t)
	ctx := context.Background()

	b, err := f.Fetch(ctx, Spec{URL: srv.URL + "/ok"})
	if err != nil || string(b) != "hello\n" {
		t.Fatalf("ok: %q %v", b, err)
	}
	if ua, _ := gotUA.Load().(string); ua != "rulegen-test/1" {
		t.Errorf("user agent = %q", ua)
	}
	b, err = f.Fetch(ctx, Spec{URL: srv.URL + "/flaky"})
	if err != nil || string(b) != "recovered" {
		t.Fatalf("flaky: %q %v (attempts %d)", b, err, flaky.Load())
	}
	if _, err = f.Fetch(ctx, Spec{URL: srv.URL + "/missing"}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404: err = %v", err)
	}
	if _, err = f.Fetch(ctx, Spec{URL: srv.URL + "/big"}); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("big: err = %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = f.Fetch(cctx, Spec{URL: srv.URL + "/ok"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: err = %v", err)
	}
}

func TestFetchRetriesExhausted(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	f := newFetcher(t)
	if _, err := f.Fetch(context.Background(), Spec{URL: srv.URL}); err == nil {
		t.Fatal("expected error")
	}
	if got := n.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestFetchFileAndInline(t *testing.T) {
	f := newFetcher(t)
	if err := os.WriteFile(filepath.Join(f.BaseDir, "list.txt"), []byte("a.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := f.Fetch(context.Background(), Spec{File: "list.txt"})
	if err != nil || string(b) != "a.com\n" {
		t.Fatalf("file: %q %v", b, err)
	}
	if _, err = f.Fetch(context.Background(), Spec{File: "missing.txt"}); err == nil {
		t.Fatal("missing file should error")
	}
	b, err = f.Fetch(context.Background(), Spec{Inline: "x.com\ny.com\n"})
	if err != nil || string(b) != "x.com\ny.com\n" {
		t.Fatalf("inline: %q %v", b, err)
	}
	if _, err = f.Fetch(context.Background(), Spec{}); err == nil {
		t.Fatal("empty spec should error")
	}
	if _, err = f.Fetch(context.Background(), Spec{File: "a", Inline: "b"}); err == nil {
		t.Fatal("ambiguous spec should error")
	}
}

func TestDecode(t *testing.T) {
	plain := "[AutoProxy 0.2.9]\n||a.com\n"
	enc := base64.StdEncoding.EncodeToString([]byte(plain))
	wrapped := enc[:10] + "\n" + enc[10:] + "\n"

	if got, err := Decode([]byte(wrapped), "base64"); err != nil || string(got) != plain {
		t.Fatalf("base64: %q %v", got, err)
	}
	if got, err := Decode([]byte(wrapped), "auto"); err != nil || string(got) != plain {
		t.Fatalf("auto: %q %v", got, err)
	}
	if got, err := Decode([]byte(plain), "auto"); err != nil || string(got) != plain {
		t.Fatalf("auto plain: %q %v", got, err)
	}
	if got, err := Decode([]byte("abcd\nefgh\n"), "auto"); err != nil || string(got) != "abcd\nefgh\n" {
		t.Fatalf("auto ambiguous text must stay: %q %v", got, err)
	}
	if got, err := Decode([]byte(plain), ""); err != nil || string(got) != plain {
		t.Fatalf("plain: %q %v", got, err)
	}
	if _, err := Decode([]byte("not base64!"), "base64"); err == nil {
		t.Fatal("bad base64 should error")
	}
	if _, err := Decode([]byte("x"), "rot13"); err == nil {
		t.Fatal("unknown encoding should error")
	}
}

func TestFetchDecodes(t *testing.T) {
	f := newFetcher(t)
	enc := base64.StdEncoding.EncodeToString([]byte("||a.com\n"))
	b, err := f.Fetch(context.Background(), Spec{Inline: enc, Encoding: "base64"})
	if err != nil || string(b) != "||a.com\n" {
		t.Fatalf("got %q %v", b, err)
	}
	if got := (Spec{URL: "http://x/y"}).String(); got != "http://x/y" {
		t.Errorf("String = %q", got)
	}
	if got := (Spec{Inline: "a\nb"}).String(); got != "inline" {
		t.Errorf("String = %q", got)
	}
}
