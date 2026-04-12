# Install webp-guard

This archive is the fast path:

1. Put `webp-guard` (or `webp-guard.exe`) somewhere on your `PATH`
2. Run `webp-guard version`
3. Run `webp-guard help`
4. Try `webp-guard scan --dir ./assets`
5. Try `webp-guard bulk --dir ./assets --dry-run`
6. Run `webp-guard init` if you want shorter commands after the first pass
7. Install `cwebp` only when you are ready for non-dry-run `bulk` or `resume`

`scan`, `verify`, `plan`, `publish`, `verify-delivery`, and `bulk -dry-run` work even before `cwebp` is installed.

## Install cwebp

- macOS: `brew install webp`
- Ubuntu / Debian: `sudo apt-get update && sudo apt-get install -y webp`
- Windows: download the precompiled WebP utilities from <https://developers.google.com/speed/webp/download> and add the extracted `bin` directory to `PATH`

After `webp-guard init`, adjust `webp-guard.toml` once and later runs can stay as short as `webp-guard bulk` or `webp-guard verify`.

For the full guide and support matrix, see `README.md`.
