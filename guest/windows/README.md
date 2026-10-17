# Windows guest helper

`desktop-bridge.ps1` reads processes, top-level windows, and controls exposed by
Microsoft UI Automation (UIA). It opens no network port and provides no arbitrary
command, click, input, screenshot, text/value-pattern, or elevation API.

[Installation and CLI use](../../docs/USAGE.md) ·
[Validation guide](../../docs/ACCEPTANCE.md) ·
[Desktop test fixture](../../docs/DESKTOP_TESTING.md)

## Development actions

The separate `developer-bridge.ps1` entry and adjacent `developer/` directory
provide registered development actions. Copy both to the same installation root,
preserving nested worker directories:

```powershell
New-Item -ItemType Directory -Path 'C:\ProgramData\Starbridge' -Force | Out-Null
Copy-Item .\guest\windows\developer-bridge.ps1 'C:\ProgramData\Starbridge\'
Copy-Item .\guest\windows\developer 'C:\ProgramData\Starbridge\' -Recurse -Force
```

Use `starbridge dev system.info VM` to inspect environment and installed actions.
Write actions require explicit authorization. See the
[developer protocol](../../docs/DEVELOPER.md) for CLI and module contracts.
Replace the complete helper tree when upgrading; copying over an old directory
can retain removed modules, which `system.info` will still report as installed.

## Runtime and session

Windows PowerShell 5.1 and the built-in .NET Framework UI Automation assemblies
are required. The entry script can also run in PowerShell 7 on Windows; UIA uses
an isolated Windows PowerShell worker.

Run desktop queries in the logged-in user's interactive session. `windows.list`
and `ui.tree` reject Session 0 and noninteractive contexts with
`INTERACTIVE_SESSION_REQUIRED`. The session metadata, desktop name, and a
successful process query do not prove access to the intended visible desktop.
Check a known window and its controls through the actual Parallels transport.
Locked desktops, secure UAC desktops, elevated applications, and missing or
incomplete accessibility providers may prevent inspection. The helper does not
change UAC, UIAccess, or desktop security settings.

## Request and response

Pass Base64 of a UTF-8 JSON object using `-RequestBase64`. `action` is required;
`params` is an object and defaults to `{}`. Base64 is encoding, not encryption.

```powershell
$request = @{ action = 'windows.list'; params = @{ limit = 100 } } | ConvertTo-Json -Compress
$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($request))
& powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass `
  -File 'C:\ProgramData\Starbridge\desktop-bridge.ps1' -RequestBase64 $encoded
```

`-ExecutionPolicy Bypass` is process-scoped; organization policy can still block
execution. Requests are limited to 16,000 decoded bytes and 24,000 Base64 characters.

The host accepts exact integer-valued JSON numbers such as `12.0` and `1.2e1`,
then sends ordinary integers (`12`) and explicit defaults. The helper accepts
only integer tokens, not decimal/exponent tokens or numeric strings. Direct
callers must normalize numbers before sending them. Name-filter and managed
text limits count Unicode code points: an emoji counts as one, and truncation
preserves complete surrogate pairs. Win32 title/class buffers instead count
UTF-16 code units.

Each completed invocation writes one compact UTF-8 JSON response to stdout.
Success exits `0`; a helper error exits `1`:

```json
{"ok":true,"data":{"processes":[],"truncated":false,"limit":500},"meta":{"protocol_version":"1","action":"processes.list","timestamp":"2026-10-18T00:00:00.0000000Z","duration_ms":100,"user":"EXAMPLE\\User","session_id":1,"interactive":true,"user_interactive":true,"desktop":null,"coordinate_space":null,"read_only":true}}
```

```json
{"ok":false,"error":{"code":"WINDOW_NOT_FOUND","message":"No window with this hwnd is available in the current session."}}
```

Success `meta` contains the protocol version (`"1"`), requested action, UTC
`timestamp`, `duration_ms`, `user`, `session_id`, `interactive` and its equivalent
`user_interactive`, `desktop` (string or `null`), `coordinate_space`, and
`read_only:true`. The desktop name describes the calling thread, not necessarily
the visible input desktop. Process-only coordinates are `null`. UIA metadata
comes from the worker, so its duration excludes outer-process startup.

The host rejects envelopes with both `data` and `error`, missing required fields,
or invalid field types. Failure needs nonempty string `error.code` and
`error.message`; successful `data` is an object matching the selected action.
Returned counts and depths must fit declared bounds, and declared bounds cannot
exceed the normalized host request. Extra fields are allowed. PowerShell startup
or parameter-binding failures can occur before the helper runs; callers must
also handle a nonzero exit with no JSON.

## Actions

### `processes.list`

```json
{"action":"processes.list","params":{"limit":500,"name":"notepad","include_path":false}}
```

`limit` defaults to `500`, range `1..1000`. Optional `pid` filters one process;
the host accepts `1..2147483647`, while the direct helper also accepts `0` for no
PID filter. Omit `pid` to list all processes through either interface. Optional
`name` is a case-insensitive substring, at most 256 code points; both filters
apply when supplied. `include_path` defaults to `false`.

`data` contains `processes`, boolean `truncated`, and integer `limit`. Processes
are sorted by PID; an absent or exited requested PID yields an empty array.
Each item contains:

- `pid`, `name` (up to 512 code points).
- `session_id`, `start_time_utc`: `null` when unavailable. `identity_status` is
  `available` when both were read, otherwise `partial`.
- `path`: `null` unless requested and readable, capped at 2048 code points.
  `path_status` is `not_requested`, `available`, or `unavailable`.

`truncated:true` means an extra matching process was omitted. Process command
lines and environment variables are not read. Start time can help identify
reused PIDs; the result remains a snapshot subject to races.

### `windows.list`

```json
{"action":"windows.list","params":{"limit":500,"include_hidden":false}}
```

`limit` and `pid` use the process-list rules. `include_hidden` defaults to
`false`, returning only windows whose Win32 visibility flag is set. This does
not prove that a window is unoccluded, painted, or on the current virtual desktop.

`data` contains `windows`, `truncated`, `limit`, `dpi_awareness`,
`coordinate_space`, and `rect_source:"GetWindowRect"`. Each window contains
`hwnd`, `pid`, `process_name` (possibly `null`), `title`, `class_name`,
`is_visible`, and `rect:{x,y,width,height}`. HWNDs are strings such as `"0x30A52"`;
title/class buffers are capped at 512/256 UTF-16 code units.

Enumeration covers top-level desktop-app windows, not child controls or every
application surface. `GetWindowRect` may include invisible resize borders.
Coordinates are `guest-screen-physical-pixels` when a per-monitor DPI context
is established, otherwise `guest-screen-dpi-context-dependent-pixels`. Negative
coordinates are valid on monitors left of or above the primary display. These
are Windows guest coordinates, not macOS or Parallels viewer coordinates.

### `ui.tree`

Obtain a fresh HWND from `windows.list`:

```json
{"action":"ui.tree","params":{"hwnd":"0x30A52","max_depth":6,"max_nodes":300,"timeout_ms":10000}}
```

- `hwnd` is required. The host accepts ASCII decimal or `0x`/`0X` hexadecimal
  strings in `1..0x7fffffffffffffff`, including leading zeroes, then sends
  lowercase hexadecimal without leading zeroes (`000000000000000000001` becomes
  `0x1`). Direct helper input is limited to 20 decimal or 16 hexadecimal digits;
  use the same short form. The handle must fit the Windows process's pointer size.
- `max_depth`: default `6`, range `0..12`; the root has depth `0`.
- `max_nodes`: default `300`, range `1..1000`, including the root.
- `timeout_ms`: default `10000`, range `1000..20000`, covering worker startup
  and inspection. Timeout returns `UIA_TIMEOUT` after attempting worker termination.
  Clients must not use the private `-UiWorker` switch, which bypasses that timeout.

`data` contains `hwnd`, `snapshot_id`, `view:"control"`, `nodes`, `warnings`,
`max_nodes`, `max_depth`, `node_limit_reached`, `depth_limit_reached`,
`coordinate_space:"guest-screen-physical-pixels"`, and `dpi_awareness`.
`nodes` is a flat, depth-first Control View tree beginning with the requested root:

```json
{"node_id":"n000002","parent_id":"n000001","depth":1,"runtime_id":[42,1234,7],"name":"Save","control_type":"Button","automation_id":"SaveButton","class_name":"Button","framework_id":"Win32","pid":1200,"rect":{"x":480,"y":320,"width":90,"height":30},"is_enabled":true,"is_offscreen":false,"is_password":false}
```

`node_id` is unique within its `snapshot_id`; the root's `parent_id` is `null`.
`runtime_id` is an opaque integer array or `null`, not a durable selector.
`automation_id` may be empty or repeated. Names and automation IDs are capped at
512 code points, class names at 256, and framework IDs at 128. Refresh before
using an identifier: HWNDs, PIDs, and UIA IDs may disappear or be reused.

`node_limit_reached` means additional nodes were omitted. `depth_limit_reached`
means a returned node reached the configured depth; it does not prove that node
has children. `warnings` contains up to 16 deduplicated codes:
`runtime_id_unavailable`, `element_unavailable_or_access_denied`,
`children_unavailable_or_access_denied`, or `siblings_unavailable_or_access_denied`.

UIA rectangles are physical screen pixels; empty or unusable rectangles are
`null`. Neither `is_offscreen:false` nor a nonempty rectangle proves clickability.
Custom-drawn apps, games, remote canvases, or incomplete accessibility providers
may expose only an outer window. No OCR or ValuePattern/TextPattern content is read.

## Errors and validation

Helper errors are `UNKNOWN_ACTION`, `INVALID_REQUEST`, `UNSUPPORTED_PLATFORM`,
`INTERACTIVE_SESSION_REQUIRED`, `WINDOW_NOT_FOUND`, `UIA_UNAVAILABLE`,
`UIA_TIMEOUT`, `UIA_WORKER_UNAVAILABLE`, `UIA_WORKER_FAILED`, or `INTERNAL_ERROR`.
Errors omit raw exceptions. Successful metadata and UI content can still reveal
user names, application/document titles, labels, and requested executable paths.

See [validation and acceptance](../../docs/ACCEPTANCE.md) for automated check
coverage, and [the desktop fixture](../../docs/DESKTOP_TESTING.md) for visible
window/UIA checks. Automated checks live in
[`guest-smoke.ps1`](../../tests/windows/guest-smoke.ps1) and
[`parameter-contract.ps1`](../../tests/windows/parameter-contract.ps1).

For timings, run [`guest-performance.ps1`](../../tests/windows/guest-performance.ps1)
in Windows PowerShell 5.1. `-Hwnd` samples a fresh window's UIA tree at node limits
`1,25,100,250,1000`; `-OutputPath` saves CSV. `wall_ms` includes process startup and
output, while `helper_ms` uses metadata duration. Compare the same environment
and window; the script does not launch a VM or application or assert a speedup.

## API references

- [EnumWindows](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-enumwindows)
- [GetWindowRect and DPI virtualization](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getwindowrect)
- [UIA physical screen coordinates](https://learn.microsoft.com/en-us/dotnet/api/system.windows.automation.automationelement.boundingrectangleproperty)
- [UIA runtime IDs](https://learn.microsoft.com/en-us/dotnet/api/system.windows.automation.automationelement.getruntimeid)
- [UIPI and UIAccess](https://learn.microsoft.com/en-us/windows/security/threat-protection/security-policy-settings/user-account-control-allow-uiaccess-applications-to-prompt-for-elevation-without-using-the-secure-desktop)
