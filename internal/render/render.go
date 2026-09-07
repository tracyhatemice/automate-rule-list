// Package render turns intermediate lines into output files. Each
// Renderer knows one output syntax and which job kinds it can render;
// renderers register themselves by name so new formats can be added
// without touching the pipeline.
package render

import (
	"fmt"
	"io"
	"slices"
	"sync"
)

// Header holds the comment lines written before the entries.
type Header struct {
	Lines     []string // extra comment lines, without the leading "# "
	Timestamp string   // rendered as "# last updated: …"; empty omits it
}

// Renderer renders intermediate lines of a job kind.
type Renderer interface {
	Name() string
	Supports(kind string) bool
	Render(w io.Writer, kind string, hdr Header, lines []string) error
}

var (
	mu       sync.RWMutex
	registry = map[string]Renderer{}
)

// Register adds a renderer, replacing any renderer with the same name.
func Register(r Renderer) {
	mu.Lock()
	defer mu.Unlock()
	registry[r.Name()] = r
}

// Lookup returns the renderer registered under name.
func Lookup(name string) (Renderer, bool) {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := registry[name]
	return r, ok
}

// Names returns the registered renderer names, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// WriteHeader writes hdr as "# …" comment lines.
func WriteHeader(w io.Writer, hdr Header) error {
	for _, l := range hdr.Lines {
		if _, err := fmt.Fprintf(w, "# %s\n", l); err != nil {
			return err
		}
	}
	if hdr.Timestamp != "" {
		if _, err := fmt.Fprintf(w, "# last updated: %s\n", hdr.Timestamp); err != nil {
			return err
		}
	}
	return nil
}

// lineRenderer renders every line through a per-kind formatter.
type lineRenderer struct {
	name    string
	kinds   map[string]bool
	prelude string
	empty   string // written instead of prelude when there are no lines
	format  func(kind, line string) string
}

func (r lineRenderer) Name() string              { return r.name }
func (r lineRenderer) Supports(kind string) bool { return r.kinds[kind] }

func (r lineRenderer) Render(w io.Writer, kind string, hdr Header, lines []string) error {
	if !r.Supports(kind) {
		return fmt.Errorf("renderer %q does not support %q jobs", r.name, kind)
	}
	if err := WriteHeader(w, hdr); err != nil {
		return err
	}
	if len(lines) == 0 && r.empty != "" {
		_, err := io.WriteString(w, r.empty)
		return err
	}
	if r.prelude != "" {
		if _, err := io.WriteString(w, r.prelude); err != nil {
			return err
		}
	}
	for _, l := range lines {
		if _, err := io.WriteString(w, r.format(kind, l)+"\n"); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	identity := func(_, line string) string { return line }
	Register(lineRenderer{name: "plain", kinds: map[string]bool{"domain": true, "ip": true}, format: identity})
	Register(lineRenderer{name: "iplist", kinds: map[string]bool{"ip": true}, format: identity})
	Register(lineRenderer{name: "smartdns", kinds: map[string]bool{"domain": true}, format: func(_, line string) string {
		return "address /" + line + "/#"
	}})
	Register(lineRenderer{
		name:    "clash",
		kinds:   map[string]bool{"domain": true, "ip": true, "clash": true},
		prelude: "payload:\n",
		empty:   "payload: []\n",
		format:  clashLine,
	})
}
