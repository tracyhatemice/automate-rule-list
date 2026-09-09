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
        behavior: classical             # clash only: classical | domain | ipcidr (see Output formats)
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
# run once: fetch, process, and upload changed outputs
rulegen run -c rulegen.yaml
# keep running: repeat every 6 hours (30m, 6h, 1d …) until SIGINT/SIGTERM
rulegen run -c rulegen.yaml --every 6h
```

With `--every` the first run starts immediately, the interval is measured
from the start of each run, the config file is re-read every cycle so
edits apply without a restart, and a failed cycle is logged and retried at
the next one. The minimum interval is one minute.

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

Compose file for a long-running container that refreshes the lists daily:

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
    command: ["run", "-c", "/work/rulegen.yaml", "--every", "1d"]
    restart: unless-stopped
    logging:
      driver: json-file
      options: { max-size: "10m", max-file: "3" }
```

```sh
docker compose run --rm rulegen check -c /work/rulegen.yaml   # try the config once
docker compose up -d                                          # start the scheduler
docker compose logs -f                                        # watch runs
docker compose pull && docker compose up -d                   # update the image
```

Without `--every` the container exits after one run, which suits a cron
entry or a systemd timer if you prefer external scheduling.

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
- `clash`: rule-provider YAML. With the default `behavior: classical`,
  domains become `DOMAIN-SUFFIX,…`, addresses `IP-CIDR,…,no-resolve`, and
  clash rules are kept (scalars single-quoted). `behavior: domain` writes
  `'+.host'` entries and `behavior: ipcidr` bare `'10.0.0.0/8'` entries,
  the trie- and set-backed formats mihomo matches much faster for large
  lists; the consuming rule-provider must declare the same `behavior`.
  A `clash` job can use them too when every rule converts: `DOMAIN` →
  `host`, `DOMAIN-SUFFIX` → `+.host`, `DOMAIN-WILDCARD` and quoted scalars
  as they are (`*` and `+` must be whole labels, `+` only first), and
  `IP-CIDR` → the block. A keyword, regex, GEOSITE or IP rule in a
  `domain` output (or a domain rule in an `ipcidr` output) fails the job
  naming the rule.

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
