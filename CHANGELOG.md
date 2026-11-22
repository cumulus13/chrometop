# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/) and the project uses [Semantic Versioning](https://semver.org/).

## [1.1.0]

### Fixed

- **Layout:** the table height now follows the terminal height. The footer sits on the last row and no space is left over. The layout is computed from real component heights instead of a hard-coded offset.
- **Layout:** rows no longer overflow or wrap. Cells are padded by display width, so emoji and CJK titles stay aligned, and every line is truncated to the terminal width.
- **Freeze:** every hub update used to start an additional 1 s tick chain, so the redraw rate grew over time. There is now exactly one tick chain.
- **Freeze:** tab commands and `SYNC` no longer run network I/O or a full poll inside the UI event loop.
- **Freeze / deadlock risk:** the hub no longer calls `Program.Send` from backend goroutines; it signals a coalescing channel instead.
- **Freeze:** one slow or blocked page can no longer stall a whole CDP polling cycle. Cycles have a time budget, each page has a reader goroutine, and unresponsive pages back off.
- **Flicker:** a single failed CDP poll no longer empties the table. The hub goes offline only after 3 consecutive failures.
- Filter input now accepts spaces, and `Ctrl+C` works while filtering or inspecting.
- `.gitignore` no longer ignores the `cmd/chrometop/` source directory.

### Added

- `--version` and `--new-tab-url` flags; a warning when listening on a non-loopback address without `--token`.
- `PgUp` / `PgDn` navigation; the selected tab stays selected when rows re-sort.
- Sanitizing of page titles and URLs before printing.
- Hub `Subscribe()` API and `Snapshot.Revision`.
- GitHub Actions workflows (CI, release) and Dependabot configuration.
- Tests for layout invariants, CDP polling (with a fake Chromium) and the hub.
- Full documentation set with Mermaid diagrams.

### Changed

- Extension (1.3.0) coalesces telemetry pushes and no longer holds message channels open.
- `Makefile` and `build.ps1` embed the version and commit into the binary.

## [1.0.1]

- Previous release. See the git history for details.
