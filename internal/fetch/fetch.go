// Package fetch loads source bodies from URLs, local files or inline
// text, with retries for transient HTTP failures and optional base64
// decoding.
package fetch

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Spec says where a source lives. Exactly one of URL, File or Inline must
// be set.
type Spec struct {
	URL      string
	File     string
	Inline   string
	Encoding string // "", "plain", "base64" or "auto"
}

// String describes the spec for logs.
func (s Spec) String() string {
	switch {
	case s.URL != "":
		return s.URL
	case s.File != "":
		return "file:" + s.File
	default:
		return "inline"
	}
}

// Fetcher retrieves source bodies.
type Fetcher struct {
	Client    *http.Client
	Retries   int           // extra attempts after the first
	Backoff   time.Duration // base delay; doubles per attempt, capped at 5s
	UserAgent string
	MaxBody   int64  // bytes; 0 means 64 MiB
	BaseDir   string // relative File paths are resolved against it
	Log       *slog.Logger
}

const defaultMaxBody = 64 << 20

// Fetch returns the decoded body of s.
func (f *Fetcher) Fetch(ctx context.Context, s Spec) ([]byte, error) {
	set := 0
	for _, v := range []string{s.URL, s.File, s.Inline} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		return nil, errors.New("source needs exactly one of url, file or inline")
	}
	var (
		raw []byte
		err error
	)
	switch {
	case s.URL != "":
		raw, err = f.fetchURL(ctx, s.URL)
	case s.File != "":
		p := s.File
		if !filepath.IsAbs(p) && f.BaseDir != "" {
			p = filepath.Join(f.BaseDir, p)
		}
		raw, err = os.ReadFile(p)
	default:
		raw = []byte(s.Inline)
	}
	if err != nil {
		return nil, err
	}
	return Decode(raw, s.Encoding)
}

func (f *Fetcher) fetchURL(ctx context.Context, url string) ([]byte, error) {
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	maxBody := f.MaxBody
	if maxBody <= 0 {
		maxBody = defaultMaxBody
	}
	var lastErr error
	for attempt := 0; attempt <= f.Retries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, f.delay(attempt)); err != nil {
				return nil, err
			}
			f.logger().Debug("retrying", "url", url, "attempt", attempt+1, "err", lastErr)
		}
		body, retry, err := f.attempt(ctx, client, url, maxBody)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, ctx.Err())
	}
	return nil, fmt.Errorf("fetch %s: %w", url, lastErr)
}

// attempt performs one request. retry reports whether the failure is
// worth retrying (network errors and 5xx / 429 responses).
func (f *Fetcher) attempt(ctx context.Context, client *http.Client, url string, maxBody int64) (body []byte, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		retry = resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
		return nil, retry, fmt.Errorf("unexpected status %s", resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, true, err
	}
	if int64(len(body)) > maxBody {
		return nil, false, fmt.Errorf("body larger than %d bytes", maxBody)
	}
	return body, false, nil
}

func (f *Fetcher) delay(attempt int) time.Duration {
	base := f.Backoff
	if base <= 0 {
		base = 200 * time.Millisecond
	}
	d := base << (attempt - 1)
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d + time.Duration(rand.Int64N(int64(d)/2+1)) //nolint:gosec // jitter only
}

func (f *Fetcher) logger() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var base64Alphabet = regexp.MustCompile(`^[A-Za-z0-9+/=\s]+$`)

// Decode applies the named encoding: "" or "plain" returns data as is,
// "base64" decodes it, and "auto" decodes only when the body is made of
// base64 characters and decodes to text that looks like a list.
func Decode(data []byte, encoding string) ([]byte, error) {
	switch encoding {
	case "", "plain":
		return data, nil
	case "base64":
		return decodeBase64(data)
	case "auto":
		if !looksBase64(data) {
			return data, nil
		}
		out, err := decodeBase64(data)
		if err != nil || !utf8.Valid(out) || bytes.IndexByte(out, 0) >= 0 || !bytes.Contains(out, []byte("\n")) {
			return data, nil
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown encoding %q", encoding)
	}
}

func looksBase64(data []byte) bool {
	return len(data) >= 16 && base64Alphabet.Match(data) && !bytes.Contains(bytes.TrimSpace(data), []byte("\n\n"))
}

func decodeBase64(data []byte) ([]byte, error) {
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, string(data))
	out, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		if out2, err2 := base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "=")); err2 == nil {
			return out2, nil
		}
		return nil, fmt.Errorf("base64: %w", err)
	}
	return out, nil
}
