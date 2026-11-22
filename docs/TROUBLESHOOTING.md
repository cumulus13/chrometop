# Troubleshooting

## The table does not fill the terminal / the footer floats

Update to v1.1.0 or newer. Earlier versions reserved a fixed number of rows and could mis-measure emoji. If it still happens, check that your terminal reports its size correctly (`echo $LINES $COLUMNS`) and open an issue with the terminal name and size.

## The UI freezes for a few seconds

Fixed in v1.1.0 (see the root-cause table in [ARCHITECTURE.md](../ARCHITECTURE.md#7-why-the-ui-used-to-freeze-and-the-fixes)). If you still see stalls:

1. Run with a larger polling interval: `chrometop --cdp http://127.0.0.1:9222 --interval 2s`.
2. Check whether one tab is busy. Pages with a blocked main thread answer slowly. ChromeTop keeps their last values and backs off for up to 10 seconds.
3. Check the footer. A message such as `CDP slow to respond (1/3)` means the browser itself is slow to answer `/json/list`.

## `OFFLINE` in extension mode

- Is ChromeTop running and listening on the address the extension uses (default `127.0.0.1:9333`)?
- Was a `--token` set? The same token must be configured in the extension.
- Reload the extension at `chrome://extensions`. The service worker may have been suspended.

## `CDP unreachable`

- Start the browser with `--remote-debugging-port=9222`.
- On Chrome 136+ also pass `--user-data-dir=<non-default directory>`.
- Test the endpoint: `curl http://127.0.0.1:9222/json/version`.

## Some actions say "supported in Extension mode"

Discard, mute and pin need the extension (CDP has no equivalent). Use extension mode for them.

## Copy URL fails on Linux

Install `wl-clipboard` (Wayland) or `xclip` / `xsel` (X11).

## Metrics look too low or stay at zero

- CPU in CDP mode is main-thread task time of the page, not whole-process CPU.
- Network in CDP mode is based on resource timing, which browsers cap at a few hundred entries per page.
- MEM is a heuristic unless `chrome.processes` data is available.

## Garbled alignment

Use a terminal and font with proper emoji support. Terminals that disagree on emoji width can still shift a column by one cell.
