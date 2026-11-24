# Development and releases

Run the checks and try the offline demo.

```sh
make check
make build
./loop-tui --demo
```

Build a local release archive with the packaging script.

```sh
./scripts/package.sh linux/amd64 v0.1.0 dist
```

Pushing an annotated `v*` tag triggers the release workflow. It builds Linux and OpenBSD archives for amd64, arm64, and ARMv7, plus macOS archives for Intel and Apple Silicon. The workflow checks both macOS binaries on native runners and publishes SHA-256 checksums. The tag annotation becomes the release notes.
