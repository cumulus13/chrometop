# Security

## Model

- ChromeTop listens on `127.0.0.1` by default. Anyone who can reach the WebSocket can read tab titles and URLs and can control tabs, so keep it on loopback or set `--token`.
- The server only accepts WebSocket origins that are empty or start with `chrome-extension://`.
- CDP mode talks to the browser's remote debugging port, which grants full control of the browser. Bind it to `127.0.0.1` and never expose it to a network.
- Text from web pages is untrusted. ChromeTop strips control characters before printing it to prevent terminal escape-sequence injection.

## Reporting a vulnerability

Please do not open a public issue. Email the maintainer at cumulus13@gmail.com with details and reproduction steps.
