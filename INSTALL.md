# Install webp-guard

This archive is the fast path:

1. Put `webp-guard` (or `webp-guard.exe`) somewhere on your `PATH`
2. Install `cwebp` separately
3. Run `webp-guard version`
4. Run `webp-guard help`
5. Try `webp-guard bulk --dir ./assets --dry-run`

`scan`, `verify`, `plan`, `publish`, `verify-delivery`, and `bulk -dry-run` work even before `cwebp` is installed.

## Install cwebp

- macOS: `brew install webp`
- Ubuntu / Debian: `sudo apt-get update && sudo apt-get install -y webp`
- Windows: download the precompiled WebP utilities from <https://developers.google.com/speed/webp/download> and add the extracted `bin` directory to `PATH`

For the full guide and support matrix, see `README.md`.
