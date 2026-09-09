package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/tracyhatemice/automate-rule-list/internal/config"
	"github.com/tracyhatemice/automate-rule-list/internal/publish"
	"github.com/tracyhatemice/automate-rule-list/internal/render"
	"github.com/tracyhatemice/automate-rule-list/internal/state"
)

const contentType = "text/plain; charset=utf-8"

// syncOutputs implements change detection and brings every output up to
// date. When the intermediate lines changed (or the run is forced) the
// timestamp is renewed and every output is rewritten and uploaded.
// Otherwise the stored timestamp is reused and only outputs that are
// missing, stale or never uploaded are touched.
func (r *Runner) syncOutputs(ctx context.Context, job config.Job, lines []string, res *Result, log *slog.Logger) error {
	hash := state.HashLines(lines)
	prev, found, err := r.Store.Load(job.Name)
	if err != nil {
		return err
	}
	if !found {
		prev = &state.State{Outputs: map[string]state.OutputState{}}
	}
	res.Changed = r.Force || !found || prev.Hash != hash
	timestamp := prev.UpdatedAt
	if res.Changed || timestamp == "" {
		timestamp = r.now().UTC().Format(r.Cfg.TimestampLayout)
	}
	switch {
	case !found:
		log.Info("first run", "entries", len(lines))
	case res.Changed:
		log.Info("content changed", "entries", len(lines), "previous_update", prev.UpdatedAt)
	default:
		log.Info("content unchanged", "entries", len(lines), "last_update", prev.UpdatedAt)
	}

	next := &state.State{Hash: hash, UpdatedAt: timestamp, Outputs: map[string]state.OutputState{}}
	if res.Changed && !r.DryRun {
		if err := r.Store.WriteIntermediate(job.Name, lines); err != nil {
			return err
		}
	}
	var errs []error
	for _, out := range job.Outputs {
		st, err := r.syncOutput(ctx, job, out, lines, timestamp, res.Changed, prev.Outputs[out.Path], res, log)
		if err != nil {
			errs = append(errs, err)
		}
		next.Outputs[out.Path] = st
	}
	if r.DryRun {
		return errors.Join(errs...)
	}
	if err := r.Store.Save(job.Name, next); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (r *Runner) syncOutput(ctx context.Context, job config.Job, out config.Output, lines []string, timestamp string, changed bool, prev state.OutputState, res *Result, log *slog.Logger) (state.OutputState, error) {
	renderer, ok := render.Lookup(out.Format)
	if !ok {
		return prev, fmt.Errorf("output %s: unknown format %q", out.Path, out.Format)
	}
	hdr := render.Header{Lines: out.Header}
	if out.WithTimestamp() {
		hdr.Timestamp = timestamp
	}
	var buf bytes.Buffer
	if err := renderer.Render(&buf, job.Kind, hdr, lines, render.Options{Behavior: out.Behavior}); err != nil {
		return prev, fmt.Errorf("output %s: %w", out.Path, err)
	}
	body := buf.Bytes()
	hash := state.HashBytes(body)
	st := state.OutputState{Hash: hash}
	if prev.Hash == hash {
		st.UploadedTo = prev.UploadedTo // those URIs already hold this exact content
	}
	path := r.Cfg.Resolve(out.Path)

	needWrite := changed || prev.Hash != hash || !fileHasContent(path, hash)
	if needWrite {
		res.Written = append(res.Written, out.Path)
		if !r.DryRun {
			if err := state.WriteFileAtomic(path, body); err != nil {
				return st, fmt.Errorf("output %s: %w", out.Path, err)
			}
			log.Info("wrote", "path", out.Path, "bytes", len(body))
		}
	} else {
		res.Skipped = append(res.Skipped, out.Path)
	}

	if len(out.S3) == 0 {
		st.UploadedTo = nil
		return st, nil
	}
	var errs []error
	var uploaded state.UploadList
	for _, target := range out.S3 {
		bucket := target.Bucket
		if bucket == "" {
			bucket = r.Cfg.S3.Bucket
		}
		key := r.Cfg.S3.Prefix + target.Key
		uri := "s3://" + bucket + "/" + key
		if !changed && st.UploadedTo.Has(uri) {
			uploaded = append(uploaded, uri) // current content is already there
			continue
		}
		switch {
		case bucket == "":
			log.Warn("upload skipped: no S3 bucket configured", "path", out.Path, "key", key)
			continue
		case r.NoUpload:
			log.Info("upload skipped (--no-upload)", "path", out.Path, "key", key)
			continue
		case r.Publisher == nil:
			log.Warn("upload skipped: no publisher", "path", out.Path, "key", key)
			continue
		case r.DryRun:
			res.Uploaded = append(res.Uploaded, uri)
			continue
		}
		got, err := r.Publisher.Put(ctx, publish.Object{Bucket: bucket, Key: key, Body: body, ContentType: contentType})
		if err != nil {
			errs = append(errs, fmt.Errorf("output %s: %w", out.Path, err))
			continue
		}
		log.Info("uploaded", "path", out.Path, "uri", got)
		res.Uploaded = append(res.Uploaded, got)
		uploaded = append(uploaded, got)
	}
	st.UploadedTo = uploaded
	return st, errors.Join(errs...)
}

// fileHasContent reports whether path exists and hashes to hash.
func fileHasContent(path, hash string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return state.HashBytes(b) == hash
}
