# Window screenshots

Install the complete [developer helper directory](../guest/windows/README.md).
`screenshot.capture` is an interactive, read-only developer action. It requests
one PNG from an identified top-level window without activating or restoring it.
It returns the image in JSON; saving evidence on the host is a separate action.

Save a request as `capture.json`, replacing the synthetic identity with one from
your disposable test application:

```json
{"hwnd":"0x123","pid":42,"start_time_utc":"2026-01-02T03:04:05.1234567Z","max_pixels":8388608,"max_bytes":4194304}
```

```sh
./bin/starbridge dev screenshot.capture VM --request capture.json
```

Use a freshly observed HWND, PID and process start time, as for
[UI queries](DEVELOPER_UI_QUERY.md). An HWND or PID alone is insufficient because
Windows can reuse both. All three fields are required; the helper checks them
before and after rendering. The caller and target must share the same Windows
session and accessible input desktop. Session 0, a locked or inaccessible desktop,
hidden windows and minimized windows are rejected. A disabled window may still
be captured.

The only optional parameters are `timeout_ms` (1,000–20,000; default 10,000),
`max_pixels` (1–16,777,216; default 8,388,608), and `max_bytes` (1,024–4,194,304;
default 4,194,304). Each dimension is also capped at 16,384 pixels. Excess budgets,
unknown fields and host paths are rejected before transport. The helper checks
dimensions before allocating the bitmap, limits the encoded stream while writing,
and isolates the rendering call in a worker with a process deadline. No capture
retry or automatic image scaling occurs.

The result contains:

- `hwnd`, `pid`, `start_time_utc`: the requested target identity.
- `capture_started_at`, `timestamp`: UTC times immediately before and after the
  rendering call and PNG encoding; these bound an observation, not an atomic
  snapshot of the entire application.
- `rect` (`x`, `y`, `width`, `height`), `width`, `height`, and
  `coordinate_space:"guest-screen-physical-pixels"`: the full window rectangle,
  sampled under a per-monitor DPI context. It may include invisible resize
  margins. The helper rejects a move or resize detected after rendering. PNG
  pixel `(0,0)` corresponds to this window rectangle's `(x,y)`.
- `png_base64`, `byte_length`, `sha256`: PNG bytes, compressed byte count and
  lowercase SHA-256 of those exact bytes. The host validates the hash, complete
  PNG, dimensions, identity and caller budgets.
- `capture_method:"print_window"` and
  `warnings:["application-rendered-best-effort"]`.

The image is supplied by the target application through
[PrintWindow](https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-printwindow),
with full-window flags `0`. The call is synchronous and may block; the worker
deadline bounds that wait. Some GPU-rendered or otherwise unsupported applications
can return blank, partial or stale images even when the API reports success.
Starbridge cannot infer visual correctness from a valid PNG. This action neither
copies the entire desktop nor falls back to a desktop-region capture. A covered
window can therefore be requested without copying the covering application's
screen pixels, but its provider may still render incomplete content.

Screenshots contain visible text, including any sensitive content drawn by the
application. UI query password redaction does not redact images. Captures are
observations and can overlap writes from other processes; the geometry checks
cannot detect every content change. Coordinate virtualization details follow
[GetWindowRect](https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-getwindowrect).

`tests/windows/developer-screenshot-contract.ps1` compiles the native helpers and
encodes generated pixels to verify PNGs, hashes, dimensions and byte budgets.
It captures no actual windows. Acceptance against a disposable fixture window
and Parallels remains an explicit, opt-in check; synthetic tests do not establish
compatibility with arbitrary desktop software.
