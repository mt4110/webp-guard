# WebP Guard

[日本語](./README.md)

This is the English companion README. The primary README for this repository is [README.md](./README.md).

`webp-guard` is a beta Go CLI for safe, resumable bulk image scanning, WebP generation, and cache-first delivery planning.
It keeps the original asset, writes `.webp` next to it by default or under `-out-dir` when you want a clean artifact tree, and discards the candidate when the output is larger than the source.

Related design note:

- [Release Workflow And Installation Design](./docs/release-installation-design.md) (Japanese primary)

## Installation

### Recommended: Release Binary

For the first run, the primary path is to download an archive from [GitHub Releases](https://github.com/mt4110/webp-guard/releases/latest) and place `webp-guard` on your `PATH`.

The first checks can happen before `cwebp` is installed.

```bash
webp-guard version
webp-guard help
webp-guard scan --dir ./assets
webp-guard bulk --dir ./assets --dry-run
```

Install `cwebp` right before you move on to real conversions.

- macOS: `brew install webp`
- Ubuntu / Debian: `sudo apt-get update && sudo apt-get install -y webp`
- Windows: download the [Google WebP utilities](https://developers.google.com/speed/webp/download) and add the extracted `bin` directory to `PATH`

### Go Install

Use this when you already manage your own Go toolchain.

```bash
go install github.com/mt4110/webp-guard@latest
```

`cwebp` is still required separately for real conversions.

### Nix

The reproducible contributor path stays available.

```bash
nix develop
go test ./...
go build -o webp-guard .
```

### Support Matrix

| Target | Phase 1 validation |
| --- | --- |
| macOS arm64 | release build/package + native smoke |
| macOS amd64 | release build/package |
| Linux amd64 | release build/package + native smoke |
| Windows amd64 | release build/package + native smoke |
| Linux arm64 | backlog |

### When `cwebp` Is Required

| Command | `cwebp` |
| --- | --- |
| `scan` | Not required |
| `verify` | Not required |
| `plan` | Not required |
| `publish` | Not required |
| `verify-delivery` | Not required |
| `bulk` | Required unless `-dry-run` is used |
| `resume` | Required unless `-dry-run` is used |

## Quick Start

### First pass

Start with the path that still works before `cwebp` is installed.

```bash
webp-guard version

webp-guard scan --dir ./assets --report ./out/scan.jsonl

webp-guard bulk \
  --dir ./assets \
  --out-dir ./out/assets \
  --dry-run \
  --report ./out/bulk-plan.jsonl
```

### Config-first, shorter daily flow

If you do not want to repeat long flag lists, generating config first is the shortest path.

```bash
webp-guard init
# adjust webp-guard.toml for the project

webp-guard bulk --dry-run
webp-guard bulk
webp-guard verify
```

Once the config matches the project, day-to-day runs can stay as short as `webp-guard bulk` and `webp-guard verify`.

## Current Repo Understanding

- The original repo was a small Go CLI centered on recursive PNG scanning and `cwebp` execution.
- The existing safe-guard idea was already good:
  - keep the original file
  - generate `.webp` side-by-side
  - drop larger outputs
- The missing pieces were bulk-oriented orchestration:
  - mixed jpg/jpeg/png input support
  - width cap / no-upscale flow
  - security scanning
  - machine-readable reports and manifests
  - resume / verify commands
  - Windows / Ubuntu wrappers that still delegate real work to the Go CLI

## What Beta Covers

- Supports only `jpg`, `jpeg`, and `png` inputs
- Rejects other image inputs such as `webp`, `gif`, `svg`, `heic`, `heif`, and `avif`
- Resizes only when width is greater than `1200`
- Never upscales
- Preserves aspect ratio
- `-aspect-variants 16:9,4:3,1:1` can generate a primary 16:9 WebP plus supporting 4:3 / 1:1 variants
- Cropping supports centered `-crop-mode safe` or explicit focus points with `-crop-mode focus --focus-x ... --focus-y ...`
- Applies JPEG EXIF orientation before conversion
- Strips metadata by default
- Keeps original files untouched
- Generates `.webp` next to the source file or mirrors them into `-out-dir`
- Rejects files with risky dimensions, file sizes, or pixel counts
- Detects extension vs magic-byte mismatches
- Detects unreadable / broken images
- Does not follow symlinks by default
- Skips hidden directories and common system/VCS directories by default
- Streams file-by-file with a worker pool instead of loading the entire set into memory
- Shows a progress bar plus ETA on interactive terminals so long-running batches stay legible
- Propagates SIGINT / SIGTERM through context cancellation and cleans staged temp files before exit
- Generates a public `release-manifest.json` without local filesystem paths
- Generates `deploy-plan.json` for a concrete environment
- Keeps `conversion-manifest.json` and `deploy-plan.json` artifact-relative instead of baking machine-specific absolute paths
- Supports `publish -dry-run=plan`, local filesystem publish, and `verify-delivery`
- Auto-discovers `webp-guard.toml` from the cwd upward so projects can share defaults
- Adds `init` to generate a starter config file
- Adds `doctor` to check config discovery, `cwebp -version`, temp-dir access, and representative config paths
- Adds `completion` to emit shell completion scripts
- Keeps human-readable logs on stderr while `-json` reserves stdout for machine-readable summaries

## Commands

```bash
webp-guard version

webp-guard scan --dir ./assets --report ./reports/scan.jsonl

webp-guard bulk \
  --dir ./assets \
  --out-dir ./out/assets \
  --cpus 4 \
  --max-width 1200 \
  --quality 82 \
  --workers auto \
  --report ./out/conversion-report.jsonl \
  --manifest ./out/conversion-manifest.json

webp-guard bulk \
  --dir ./assets \
  --out-dir ./out/assets \
  --max-width 1200 \
  --aspect-variants 16:9,4:3,1:1 \
  --crop-mode safe \
  --report ./out/seo-report.jsonl \
  --manifest ./out/seo-manifest.json

webp-guard resume \
  --dir ./assets \
  --out-dir ./out/assets \
  --resume-from ./out/conversion-report.jsonl \
  --report ./out/conversion-resume.jsonl \
  --manifest ./out/conversion-manifest-resume.json

webp-guard verify \
  --dir ./assets \
  --manifest ./out/conversion-manifest.json \
  --report ./out/verify-report.jsonl

webp-guard plan \
  --conversion-manifest ./out/conversion-manifest.json \
  --release-manifest ./out/release-manifest.json \
  --deploy-plan ./out/deploy-plan.dev.json \
  --env dev \
  --origin-provider local \
  --origin-root ./out/dev-origin

webp-guard publish \
  --plan ./out/deploy-plan.dev.json \
  --dry-run plan

webp-guard publish \
  --plan ./out/deploy-plan.dev.json \
  --dry-run off

webp-guard verify-delivery \
  --plan ./out/deploy-plan.dev.json

webp-guard init

webp-guard doctor

webp-guard help publish

webp-guard completion zsh > ~/.zsh/completions/_webp-guard

webp-guard bulk \
  --json \
  --dry-run \
  --dir ./assets > ./out/bulk-summary.json
```

Legacy compatibility is still supported:

```bash
webp-guard -dir ./assets -dry-run
```

## Important Flags

- `-dir`: root directory to scan
- `-extensions`: target extensions, limited to `jpg,jpeg,png`, default `jpg,jpeg,png`
- `-include`, `-exclude`: repeatable or comma-separated globs
- `-cpus`: logical CPU count to use, or `auto`
- `-workers`: worker count or `auto`
- `-cpus` limits Go runtime CPU usage and also caps `-workers`
- Example: `--cpus 4 --workers auto` runs with `4` workers
- `-quality`: WebP quality `0-100`, default `82`
- `-max-width`: resize threshold, default `1200`
- `-aspect-variants`: additional aspect ratios to emit; the first one becomes the primary output
- `-crop-mode`: `safe` or `focus`
- `-focus-x`, `-focus-y`: normalized `0.0-1.0` focus point used with `-crop-mode=focus`
- `-out-dir`: optional output root that mirrors the source tree for `.webp` files
- `-on-existing`: `skip`, `overwrite`, or `fail`
- `-overwrite`: legacy alias for `-on-existing=overwrite`
- `-dry-run`: plan conversion without writing files
- `-report`: write `.jsonl` or `.csv`
- `-manifest`: write the conversion manifest used by `verify` and `plan`
- `verify -dir`: optional source-root override for manifests that are already artifact-relative
- `-resume-from`: skip files already completed in a previous report, but only when the report fingerprint matches the current conversion settings
- `-follow-symlinks`: opt in to symlink traversal inside the requested root
- `-include-hidden`: scan dot directories and dot files
- `plan -conversion-manifest`: input manifest for release planning
- `plan -release-manifest`: public manifest emitted without local absolute paths
- `plan -deploy-plan`: environment-specific upload / purge / verify instructions
- `publish -dry-run`: `off`, `plan`, or `verify`
- `-config`: explicitly point to `webp-guard.toml`
- `-no-config`: disable config-file loading
- `-json`: emit the command summary as JSON on stdout while keeping human logs on stderr

## Help, Doctor, And Completion

- `webp-guard version` prints embedded build metadata
- `webp-guard help <command>` prints focused usage for one subcommand
- `webp-guard -h`, `webp-guard --help`, and `webp-guard <subcommand> -h` print usage and exit with code `0`
- `webp-guard doctor` checks config discovery, `cwebp -version`, temp-dir writability, CPU visibility, and representative config input paths
- `webp-guard completion bash|zsh|fish|powershell` emits a completion script to stdout
- `doctor -json` keeps the report machine-readable for CI or wrapper scripts

## Config File

- The default config file name is `webp-guard.toml`
- When omitted, `webp-guard` searches upward from the current working directory
- Relative paths inside the config are resolved relative to the config file itself, not the runtime cwd
- Precedence is `CLI flags > webp-guard.toml > built-in defaults`
- If you want to avoid repeating long flag lists, the shortest path is to start with `webp-guard init` and refine the starter config

## Security Scan Policy

`scan` and `bulk` both perform the same core validation before conversion.

- Reject extension / magic-byte mismatches
- Reject unsupported image formats outside PNG/JPEG(JPG)
- Reject unknown binary signatures
- Reject files over the configured size limit
- Reject images over the configured dimension limit
- Reject images over the configured pixel-count limit
- Fully decode supported files to catch corrupted data
- Skip symlinks by default
- Skip hidden directories and common system/VCS directories by default
- Keep output paths adjacent to the source file or under the declared `-out-dir` to avoid traversal-style output mistakes

Defaults:

- `max-file-size-mb=100`
- `max-pixels=80000000`
- `max-dimension=20000`
- `follow-symlinks=false`
- `include-hidden=false`

## Conversion And Delivery Artifacts

- `conversion report`:
  - `.jsonl` for streaming-friendly bulk logs
  - `.csv` for spreadsheet workflows
- `conversion manifest`:
  - JSON manifest of successful conversions
  - keeps source/output paths relative to the artifact instead of absolute machine paths
  - keeps dimensions, quality, resize policy, and size savings
  - stays internal and feeds `verify` / `plan`
- `release manifest`:
  - public JSON manifest for delivery
  - maps logical paths to immutable object keys and public paths
  - deliberately omits local filesystem paths
- `deploy plan`:
  - environment-specific upload, purge, and verify instructions
  - keeps upload inputs relative to the plan artifact, so the artifact can move between runners
  - stages immutable upload files under the artifact using their final object-key layout
- `path portability`:
  - artifact-relative manifests and plans are meant to move within the same path-resolution environment
  - treat native Windows paths such as `C:\...` and WSL2 mounts such as `/mnt/c/...` as different environments and regenerate manifests/plans when crossing that boundary
- `resume`:
  - reads a previous report and skips entries that already reached a terminal state
  - reuses only entries whose config fingerprint matches the current run
- `verify`:
  - checks source existence, output existence, current source size, output size regression, and output dimensions
- `verify-delivery`:
  - checks published URLs or local file targets from a deploy plan

## Exit Codes

- `0`: completed without scan/verify issues
- `1`: CLI/config/runtime setup error
- `2`: scan or bulk completed but found rejected/failed files
- `3`: verify completed but found mismatches

## Ubuntu / Windows Wrappers

Wrapper scripts are included, but they stay thin on purpose.
They only select the target directory and then call the Go CLI.

For day-to-day use on Windows, prefer WSL2 with Ubuntu or Debian and run `scripts/bulk-webp.sh` from there instead of relying on native Windows execution.

- Ubuntu / Linux: `scripts/bulk-webp.sh`
  - uses the first argument if present
  - otherwise tries `zenity`
  - falls back to terminal input
- Windows: `scripts/bulk-webp.ps1`
  - uses the first argument if present
  - otherwise tries a folder picker
  - falls back to `Read-Host`
  - relative path handling is aligned with the other wrapper paths, but native Windows behavior for this branch has not been verified on a real machine yet

Forwarded relative paths are resolved from the repo root in every execution path, whether the wrapper uses `go run`, a built binary, or a `PATH` binary.

Examples:

```bash
./scripts/bulk-webp.sh ./assets --report ./reports/bulk.jsonl --manifest ./reports/manifest.json
```

```powershell
./scripts/bulk-webp.ps1 C:\site\assets --report .\reports\bulk.jsonl --manifest .\reports\manifest.json
```

## Why Not ffmpeg Yet?

`ffmpeg` was considered, but not chosen for the first bulk pipeline.

- The repo already used `cwebp`, so keeping it as the encoder preserved the existing mental model
- `cwebp` keeps the dependency surface smaller for a still-image CLI
- The new work was orchestration, validation, resume, and reporting, which belong in Go rather than shell
- `ffmpeg` is still a valid future option when the project expands into broader format handling

## Local Development

### With Nix

```bash
nix develop
go test ./...
go build -o webp-guard .
```

### With Go

Install Go and `cwebp`, then:

```bash
go test ./...
go build -o webp-guard .
```

## Large Batch Notes

- Start with `scan` or `bulk -dry-run` before a large run
- Use `-out-dir ./out/assets` when you want CI or release jobs to avoid dirtying the source tree
- Keep `workers=auto` or a moderate integer unless the machine has plenty of RAM
- If you want to keep the machine responsive, set `-cpus` explicitly, for example `-cpus 4`
- Use `resume` for interrupted jobs instead of restarting from scratch
- Prefer `.jsonl` reports for 10k+ file runs
- Keep reports/manifests outside the scanned directory when possible

## DB Integration Example

If you want to push conversion results into an application database, see [example_db_upsert_batch](./example_db_upsert_batch).

- built on Go's standard `database/sql`
- executes batched multi-row `UPSERT` statements with a worker pool
- switches SQL by dialect for PostgreSQL / MySQL / SQLite
- keeps the execution flow shared and only swaps the dialect-specific `UPSERT` clause, because there is no single fully portable `UPSERT` SQL

## Not In Beta Yet

- GIF/SVG/HEIC/AVIF/WebP input support
- Metadata preservation mode
- Additional origin/CDN adapters beyond the current local/noop path
- HEIC/AVIF decoder integration
- Smarter content-based encoder selection

## License

MIT
