# rulegen

`rulegen` fetches upstream domain and ip lists, cleans, validates and
deduplicates them, and publishes the results whenever the content
changes.

## Install

Stable releases (`vX.Y.Z` tags):
- [binary releases](https://github.com/tracyhatemice/automate-rule-list/releases): `linux/amd64` and `linux/arm64` tarballs with checksums
- docker image: `ghcr.io/tracyhatemice/rulegen:latest` (also `X.Y.Z` and `X.Y` tags)

Every push to `main` refreshes the `rolling` pre-release and the
`ghcr.io/tracyhatemice/rulegen:main` image for those who want unreleased builds.

## Configuration reference

```yaml
state_dir: state                        # intermediate files and state.json per job
timestamp_layout: "2006-01-02T15:04:05Z" # Go layout, always UTC
http:
  timeout: 60s
  retries: 3
  user_agent: rulegen/1.0
  concurrency: 8                        # parallel source downloads
  max_body_bytes: 67108864
s3:
  bucket: ${RULEGEN_S3_BUCKET}          # ${VAR} is expanded from the environment
  region: ${RULEGEN_S3_REGION}
  endpoint: ""                          # MinIO / R2 / …
  prefix: ""                            # prepended to every key
  force_path_style: false

jobs:
  - name: example                       # letters, digits, . _ -
    kind: domain                        # domain | ip | clash
    mode: aggregate                     # clash only: aggregate | mirror (one source)
    order: sorted                       # sorted | source (first-seen); clash defaults to source
    aggregate: true                     # ip only
    allow_tld: false                    # domain only: accept single-label names
    sources:
      - url: https://…                  # exactly one of url / file / inline
        format: auto                    # parser name or auto
        encoding: auto                  # plain | base64 | auto
        filter:                         # same shape as the job filter below,
          exclude: [example.com]        # applied to this source's items only
          exclude_from: []
        allow_tld: false
        optional: false                 # failure or zero entries is a warning
      - file: relative/to/config.txt
      - inline: |
          a.example
    filter:
      exclude:                          # applied after merge
        - example.com                   # domain and every subdomain
        - =exact.example.com            # exact host only
        - 10.0.0.0/8                    # address or block, for ip / clash values
        - "=DOMAIN-KEYWORD,foo"         # clash: one exact rule (options ignored)
        - /^ads?[0-9]*\./               # RE2 regex; for clash also tried on "TYPE,value" text
      exclude_from:                     # sources whose items become patterns
        - file: allowlist.txt
    outputs:
      - format: smartdns                # plain | iplist | smartdns | clash
        path: dist/example.conf         # relative to the config file
        header: ["mirror of https://…"] # extra "# …" lines before the timestamp
        timestamp: true                 # "# last updated: …" line
        s3:                             # one target …
          key: smartdns/example.conf
          bucket: ""                    # overrides s3.bucket
      - format: clash
        path: dist/example.yaml
        s3:                             # … or several; each gets the same file
          - key: clash/example.yaml
          - { key: mirror/example.yaml, bucket: other-bucket }
```

Every S3 target is tracked separately in `state.json`, so adding a target
later uploads only to the new one, and a failed upload is retried on the
next run without touching the targets that succeeded.

## Run

### Binary

```sh
# validate the config
rulegen check -c rulegen.yaml
# dry-run: fetch and process, report what would change
rulegen run -c rulegen.yaml --dry-run
# run: fetch, process, and upload changed outputs
rulegen run -c rulegen.yaml
```

### Docker

Settings and credentials go in an env file (`chmod 600`):

```ini
# /srv/rulegen/.env
RULEGEN_S3_BUCKET=
RULEGEN_S3_REGION=
# leave empty for AWS; set for MinIO / R2 / …
RULEGEN_S3_ENDPOINT=
# optional key prefix, e.g. lists/
RULEGEN_S3_PREFIX=
AWS_ACCESS_KEY_ID=
AWS_SECRET_ACCESS_KEY=
# owner of ./work (id -u / id -g); Compose reads these for the user: line below
UID=1000
GID=1000
```

Compose file for a one-shot container run:

```yaml
# /srv/rulegen/compose.yaml
#
# Layout:
#   ./compose.yaml        this file
#   ./.env                S3 settings and AWS credentials (chmod 600)
#   ./work/rulegen.yaml   the config (copy from the repo)
#   ./work/locallist/     locally maintained lists referenced by file: sources
#   ./work/state/         created on first run, keep it between runs
#   ./work/dist/          rendered outputs
services:
  rulegen:
    image: ghcr.io/tracyhatemice/rulegen:latest
    container_name: rulegen
    user: "${UID:-1000}:${GID:-1000}"
    working_dir: /work
    volumes:
      - ./work:/work
    env_file: .env
    command: ["run", "-c", "/work/rulegen.yaml"]
    restart: "no"          # one-shot; scheduling is done by cron/systemd below
    logging:
      driver: json-file
      options: { max-size: "10m", max-file: "3" }
```

Try it once with `docker compose run --rm rulegen check -c /work/rulegen.yaml`,
then create a systemd service and timer to run the container on a schedule:

```ini
# /etc/systemd/system/rulegen.service
[Unit]
Description=rulegen list refresh
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
WorkingDirectory=/srv/rulegen
ExecStartPre=/usr/bin/docker compose pull --quiet
ExecStart=/usr/bin/docker compose run --rm rulegen
```

```ini
# /etc/systemd/system/rulegen.timer
[Unit]
Description=Run rulegen daily

[Timer]
OnCalendar=*-*-* 18:30:00
RandomizedDelaySec=10m
Persistent=true

[Install]
WantedBy=timers.target
```

Reload systemd and enable the timer:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now rulegen.timer
systemctl list-timers rulegen.timer           # shows the next run time
sudo journalctl -u rulegen.service -n 50     # logs of the last run
```

## Development

### Quick start

```sh
make check                 # validate rulegen.yaml and list the jobs
make dry-run               # fetch and process, report what would change
make run ARGS="--no-upload" # write state/ and dist/, skip S3
make run                   # same, and upload outputs that have an s3 key
make test                  # unit tests
make lint                  # golangci-lint
make integration           # S3 upload test against a local MinIO
make image                 # build the runtime image
```

The host only needs Docker Compose. Go's module and build caches are kept
under `.cache/` inside the project, containers run as your user, and the
runtime image is a static binary on distroless.

The shipped `rulegen.yaml` points the `pbr` and `domestic` jobs at curated
lists under `locallist/`, which is gitignored and not part of this
repository. Create that folder with your own lists, or remove those `file:`
sources, before running them; the other jobs work in a fresh clone.

To upload, set the bucket (and, for non-AWS stores, the endpoint) and give
the usual AWS credentials:

```sh
export RULEGEN_S3_BUCKET=my-bucket RULEGEN_S3_REGION=us-east-1
export AWS_ACCESS_KEY_ID=… AWS_SECRET_ACCESS_KEY=…
make run
```

### How a job runs

```
sources ─► fetch ─► decode ─► detect format ─► parse ─► normalize + validate
        ─► per-source filter ─► merge ─► job filter ─► dedupe / collapse
        ─► intermediate lines  (state/<job>/intermediate.txt)
        ─► changed since last run?
              yes: new timestamp, rewrite every output, upload
              no : reuse the stored timestamp, only repair missing or
                   never-uploaded outputs
```

The intermediate file is the canonical list; its SHA-256 is stored in
`state/<job>/state.json` together with the timestamp of the last content
change and the hash and upload URIs of every output. Runs are idempotent:
an unchanged upstream produces no writes and no uploads, a deleted output
is regenerated with the old timestamp, and a failed upload is retried on
the next run. `--force` renews the timestamp and re-uploads everything.

A required source that fails to download, or that yields no valid entry at
all (for example a mirror that now serves an error page), fails the job so
a list can never silently shrink. Mark a source `optional: true` to only
warn instead.

### Job kinds

| kind | items | dedupe | outputs |
|------|-------|--------|---------|
| `domain` | host names, lower-cased, punycode, RFC-1035 labels | parent domain wins (`example.com` drops `a.example.com`), optional reversed-label sort | `plain`, `smartdns`, `clash` |
| `ip` | IPv4/IPv6 addresses and CIDR blocks | covered blocks removed, adjacent blocks merged (`aggregate: false` to disable) | `plain`, `iplist`, `clash` |
| `clash` | rule-provider payload items | `mode: aggregate` canonicalizes, deduplicates and collapses rules; `mode: mirror` keeps one upstream payload verbatim minus comments and invalid items | `clash` |

Input formats (`format:` on a source, `auto` detects them): `domain`,
`hosts`, `adblock` (AdGuard `||host^`), `autoproxy` (gfwlist), `dnsmasq`,
`smartdns`, `v2ray`, `iplist`, `clash`. `encoding: base64` (or `auto`)
decodes base64 bodies such as gfwlist.

### Output formats

- `plain` / `iplist`: one entry per line.
- `smartdns`: `address /example.com/#`.
- `clash`: rule-provider YAML. Domains become `DOMAIN-SUFFIX,…`, addresses
  `IP-CIDR,…,no-resolve`, clash rules are kept; domain-behaviour scalars are
  single-quoted.

Every output starts with the optional `header` lines and
`# last updated: <timestamp>`.

### Layout

```
cmd/rulegen          CLI (run, check, version)
internal/config      YAML schema, defaults, validation
internal/fetch       http / file / inline sources, retries, base64
internal/parse       parser registry, one file per input format, detection
internal/domain      host normalization, validation, collapse, matcher
internal/ipset       prefix parsing, dedupe, aggregation, matcher
internal/clashrule   clash rule model, canonicalization, collapse, ordering
internal/render      renderer registry: plain, iplist, smartdns, clash
internal/state       per-job state.json and intermediate.txt
internal/publish     Publisher interface, S3, in-memory fake
internal/pipeline    job runner
testdata/fixtures    excerpts of every real upstream format
```

Adding an input format means one `extract(line) []string` function
registered in `internal/parse`; adding an output format means one
`Renderer` registered in `internal/render`. Neither touches the pipeline.
