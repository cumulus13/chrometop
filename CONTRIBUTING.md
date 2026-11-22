# Contributing

Thanks for helping improve ChromeTop.

1. Fork the repository and create a branch from `master`.
2. Make your change. Keep code, comments and documentation in English.
3. Run `make all`. It formats, vets, tests and builds.
4. Add or update tests. UI changes should extend the layout tests in `internal/ui`.
5. Update `CHANGELOG.md` and any affected documentation.
6. Open a pull request describing the problem and the fix.

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for conventions. The two most important ones: never block the UI event loop, and always measure text by display width.
