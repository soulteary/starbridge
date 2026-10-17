# Window screenshots

Install the complete [developer helper directory](../guest/windows/README.md).
`screenshot.capture` is an interactive, read-only developer action. It requests
one PNG from an identified top-level window without activating or restoring it.
It returns the image in JSON; saving evidence on the host is a separate action.

Save a request as `capture.json`, replacing the synthetic identity with one from
your disposable test application:

```json
{"hwnd":"0x123","pid":42,"start_time_utc":"2026-01-02T03:04:05.1234567Z","capture_method":"print_window","max_pixels":8388608,"max_bytes":4194304}
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

Optional `capture_method` is `print_window` (the compatible default) or
`windows_graphics_capture`. There is no automatic backend selection: failure
never retries with another backend or captures a monitor or desktop region.

Other optional parameters are `timeout_ms` (1,000–20,000; default 10,000),
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
  sampled under a per-monitor DPI context. PrintWindow uses `GetWindowRect`,
  which may include invisible resize margins. WGC uses the compositor's
  `DWMWA_EXTENDED_FRAME_BOUNDS`, excluding those invisible margins. WGC allocates
  the frame pool from `GraphicsCaptureItem.Size` and reads only the frame's valid
  `ContentSize`; these sizes can differ from the reported screen rectangle.
  Content matching DWM bounds is used directly. Content matching the physical
  `GetWindowRect` is cropped by the measured rectangle offsets to remove resize
  margins, including asymmetric margins. Other static size/origin combinations
  return `WGC_GEOMETRY_UNAVAILABLE` instead of guessing coordinates or claiming
  that the window moved. The helper
  rejects a move or resize detected after rendering. PNG
  pixel `(0,0)` corresponds to this window rectangle's `(x,y)`.
- `png_base64`, `byte_length`, `sha256`: PNG bytes, compressed byte count and
  lowercase SHA-256 of those exact bytes. The host validates the hash, complete
  PNG, dimensions, identity and caller budgets.
- The actual `capture_method` and backend-specific `warnings`. PrintWindow keeps
  `warnings:["application-rendered-best-effort"]`. WGC returns
  `["compositor-frame-best-effort","bgra8-hdr-may-clip","system-capture-border-may-appear"]`.

The image is supplied by the target application through
[PrintWindow](https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-printwindow),
with full-window flags `0`. The call is synchronous and may block; the worker
deadline bounds that wait. Some GPU-rendered or otherwise unsupported applications
can return blank, partial or stale images even when the API reports success.
Starbridge cannot infer visual correctness from a valid PNG. This action neither
copies the entire desktop nor falls back to a desktop-region capture. A covered
window can therefore be requested without copying the covering application's
screen pixels, but its provider may still render incomplete content.

## Windows Graphics Capture

Set `capture_method:"windows_graphics_capture"` to request a compositor frame of
the specified HWND. The helper uses
[CreateForWindow](https://learn.microsoft.com/windows/win32/api/windows.graphics.capture.interop/nf-windows-graphics-capture-interop-igraphicscaptureiteminterop-createforwindow)
(Windows 10 1903, build 18362 or later) and
[CreateFreeThreaded](https://learn.microsoft.com/uwp/api/windows.graphics.capture.direct3d11captureframepool.createfreethreaded),
so no DispatcherQueue, picker, SDK, WinMD projection, NuGet package or separately
installed compiler is needed. Windows PowerShell 5.1 compiles the paired C#
source using its existing .NET Framework compiler. Install the complete helper
tree, including `developer/workers/screenshot-wgc.cs`.

The backend follows Microsoft's
[frame-size and content-subrectangle guidance](https://learn.microsoft.com/windows/uwp/audio-video-camera/screen-capture):
it uses a hardware D3D11 device and a BGRA8 pool sized from the capture item,
copies one frame to a staging texture, waits within the deadline for CPU readback
and encodes only the verified visible region with the same bounded PNG stream.
Both the complete pool/content and returned image must satisfy `max_pixels` and
the dimension limit, so removing margins does not bypass allocation budgets.
Item size, DWM bounds and physical outer-window bounds are checked again after
readback; a detected change returns `WINDOW_GEOMETRY_CHANGED`. Texture extents,
valid content, crop offsets and row pitch are checked separately; undefined
texture padding is never encoded. No image scaling occurs. It preserves Windows' capture
indicator and does not request borderless capture or bypass display-affinity
restrictions. HDR colors may clip in this BGRA8 path. Compositor frames can still
be blank, stale or incomplete; a valid, correctly hashed PNG is not proof of
visual correctness.

Unsupported OS/API/device, access denial, an explicitly excluded window, device
loss, missing frame, mismatched geometry and timeout produce stable safe errors
(`WGC_OS_UNSUPPORTED`, `WGC_API_UNAVAILABLE`, `WGC_UNSUPPORTED`,
`WGC_DEVICE_UNAVAILABLE`, `WGC_ACCESS_DENIED`, `WGC_WINDOW_EXCLUDED`,
`WGC_WINDOW_UNSUPPORTED`, `WGC_DEVICE_LOST`, `WGC_FRAME_UNAVAILABLE`,
`WGC_READBACK_FAILED`, `WGC_INVALID_BUFFER`, `WGC_GEOMETRY_UNAVAILABLE`,
`WINDOW_GEOMETRY_CHANGED`, `SCREENSHOT_TIMEOUT`). The enclosing process deadline
also bounds blocked native calls and cleanup; a worker deadline can instead
report `UIA_TIMEOUT`. No error triggers a fallback capture.

Probe support without selecting or capturing a window:

```sh
./bin/starbridge dev screenshot.capabilities VM
```

This read-only action accepts only optional `timeout_ms` and is also exposed to
MCP. It reports `probe_scope:"os-api-device"`, `os_build`, both `methods` with
`available` and a safe `reason`, plus `session_id`, `user_interactive` and
`capture_requires_input_desktop:true`. API/device availability is separate from
session readiness: probing works in Session 0, while capture still requires the
identified window's accessible interactive input desktop. A positive probe does
not establish that a particular window or policy will permit capture.

Screenshots contain visible text, including any sensitive content drawn by the
application. UI query password redaction does not redact images. Captures are
observations and can overlap writes from other processes; the geometry checks
cannot detect every content change. Coordinate virtualization details follow
[GetWindowRect](https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-getwindowrect).

`tests/windows/developer-screenshot-contract.ps1` compiles the native helpers and
encodes generated pixels to verify PNGs, hashes, dimensions and byte budgets.
`tests/windows/developer-wgc-contract.ps1` compiles the actual COM/D3D adapter,
probes OS/API/device support, roundtrips generated texture pixels through real
D3D11 create/copy/flush/map/unmap calls using WARP, and verifies BGRA row pitch,
channels, dimensions and budgets. WARP is only used by this resource fixture;
production WGC requires a hardware device. These CI contracts capture no windows. Unsupported
hardware is a capability result, not a successful capture assertion.

On a disposable interactive Windows desktop, opt into actual capture of a form
created by the test itself:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\developer-wgc-acceptance.ps1 -EnableCapture
```

Without `-EnableCapture`, the script skips before creating a window. It accepts
no target HWND and keeps the image in memory; it checks known synthetic interior
pixels as well as the PNG. This fixture and Parallels acceptance remain explicit
checks. Neither generated-buffer CI nor one fixture establishes compatibility
with arbitrary desktop software.
