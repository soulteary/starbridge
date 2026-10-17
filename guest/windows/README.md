# Windows guest helper

`desktop-bridge.ps1` is a read-only Windows desktop inspector. It returns process
names, top-level window rectangles, and the controls exposed by Microsoft UI
Automation (UIA). It does not open a port, execute arbitrary commands supplied in
JSON, click controls, read text/value patterns, or request elevation.

## Runtime and session

- Windows with Windows PowerShell 5.1 and the built-in .NET Framework UI Automation
  assemblies. The entry script can also be launched from PowerShell 7 on Windows;
  `ui.tree` deliberately uses the Windows PowerShell worker.
- Run it in the logged-in desktop user's interactive session. `meta.session_id`
  and `meta.interactive` describe the helper's context; they do **not** prove
  that it can see the desired user's desktop.
- A Parallels Tools `prlctl exec` command may run under a different account,
  session, or desktop. Process enumeration succeeding does not verify desktop
  access. Check the returned session, find a known application in `windows.list`,
  and inspect its controls before treating that transport as usable. If it runs
  in a service/session 0 context, a future per-user agent and authenticated local
  IPC transport are required. This MVP does not install such an agent.
- Locked desktops, secure UAC desktops, elevated applications, and applications
  without a usable accessibility provider can return incomplete trees or errors.
  This helper does not change UAC, UIAccess, or desktop security settings.

## Invocation

Pass Base64 of a UTF-8 JSON object using `-RequestBase64`. Base64 is a transport
encoding, not encryption. The JSON has only an action and its parameters:

```powershell
$request = @{ action = 'windows.list'; params = @{ limit = 100 } } | ConvertTo-Json -Compress
$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($request))
& powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass `
  -File 'C:\Starbridge\desktop-bridge.ps1' -RequestBase64 $encoded
```

`-ExecutionPolicy Bypass` applies only to this invocation. An organization policy
can still block script execution. Install only a copy you have reviewed. The
helper's internal UIA worker uses the same process-scoped setting.

Valid invocations write exactly one compact JSON response to stdout as UTF-8.
Success exits `0`; an error exits `1` and returns a bounded public error message.
The host rejects ambiguous envelopes containing both `data` and `error`, and
requires nonempty string `error.code` and `error.message` on failure. Successful
`data` must be an object: process/window lists require their named array, boolean
`truncated` and integer `limit`; UI trees require the node and warning arrays,
snapshot identity, configured bounds and limit flags. Required item identities,
coordinates and state fields are type-checked. Extra fields remain compatible.
PowerShell's own startup or parameter-binding errors can occur before the script
runs, so callers must also handle a nonzero exit with no JSON.

```json
{"ok":true,"data":{"windows":[],"truncated":false,"limit":100,"dpi_awareness":"per_monitor_v2","coordinate_space":"guest-screen-physical-pixels","rect_source":"GetWindowRect"},"meta":{"protocol_version":"1","action":"windows.list","timestamp":"2026-10-17T00:00:00.0000000Z","duration_ms":100,"session_id":1,"interactive":true,"user_interactive":true,"read_only":true}}
```

```json
{"ok":false,"error":{"code":"WINDOW_NOT_FOUND","message":"No window with this hwnd is available in the current session."}}
```

## Actions

### `processes.list`

```json
{"action":"processes.list","params":{"limit":200,"pid":0,"name":"notepad","include_path":false}}
```

`limit` defaults to `200` and is bounded to `1..1000`. `pid` is optional; `0`
selects all processes. `name` is an optional case-insensitive substring of the
process name, capped at 256 characters. It is not a wildcard or regular
expression. Both filters apply when supplied.

`data.processes` contains `{pid,name,session_id,start_time_utc,identity_status,path,path_status}`
objects sorted by PID. Session and start-time fields are `null` if unavailable;
`identity_status` is `partial` in that case. Start time can help a later client
distinguish recycled PIDs, but every snapshot is still subject to races.
`path` defaults to `null`. With `include_path:true`, protected, inaccessible, or
exited processes can have `path_status:"unavailable"`; process command lines and
environment variables are never retrieved. `data.truncated` indicates an extra
matching process was omitted because of the limit.

### `windows.list`

```json
{"action":"windows.list","params":{"limit":200,"pid":0,"include_hidden":false}}
```

`limit` and `pid` have the same bounds as above. Only windows satisfying
`IsWindowVisible` are returned unless `include_hidden:true` is supplied. Visible
here means the Win32 visibility flag; it does not guarantee that the window is
unoccluded, on the current virtual desktop, or visibly painted.

`data.windows` contains
`{hwnd,pid,process_name,title,class_name,is_visible,rect}`. Handles are strings such
as `"0x30A52"` so JavaScript callers do not lose integer precision. Titles are
capped at 512 characters. A rectangle is `{x,y,width,height}` and can have
negative coordinates on monitors to the left or above the primary display.

`EnumWindows` enumerates top-level desktop-app windows, not all child controls or
every application surface. Rectangles come from `GetWindowRect`, which can include
invisible resize borders. A per-monitor DPI context is attempted first; if that
cannot be established, `coordinate_space` explicitly reports
`guest-screen-dpi-context-dependent-pixels`. These are Windows guest coordinates, not
macOS screen or Parallels viewer coordinates.

### `ui.tree`

Obtain a fresh handle from `windows.list` first:

```json
{"action":"ui.tree","params":{"hwnd":"0x30A52","max_depth":4,"max_nodes":250,"timeout_ms":10000}}
```

`hwnd` is required and must be a nonzero decimal or `0x` hexadecimal string.
`max_depth` defaults to `4`, range `0..12`; the root is depth `0`. `max_nodes`
defaults to `250`, range `1..1000`. `timeout_ms` defaults to `10000`, range
`1000..20000`, and covers worker startup plus inspection.

The response contains a flat, depth-first `data.nodes` array. Each node has:

```json
{"node_id":"n000002","parent_id":"n000001","depth":1,"runtime_id":[42,1234,7],"name":"Save","control_type":"Button","automation_id":"SaveButton","class_name":"Button","framework_id":"Win32","pid":1200,"rect":{"x":480,"y":320,"width":90,"height":30},"is_enabled":true,"is_offscreen":false,"is_password":false}
```

The array uses UIA's **Control View**. It includes the requested window root.
`snapshot_id` identifies the observation; `node_id` is unique only within that
observation. `runtime_id` is an opaque UIA identifier, may be `null`, and is not a
durable selector across application or UI lifetimes. `automation_id` may be empty
or repeated. Refresh before a later action; this MVP intentionally exposes no UI
mutation API.

UIA rectangles are physical screen coordinates by the API's contract; an empty
or unusable rectangle is `null`. `IsOffscreen:false` and a nonempty rectangle do
not guarantee that a point can be clicked. A custom-drawn app, game, remote canvas,
or provider with missing accessibility information may expose only its outer
window. The helper does not invent button positions or use OCR.

`node_limit_reached` means additional nodes were omitted. `depth_limit_reached`
means at least one returned node reached the configured depth, without probing
whether that node has children. Up to 16 deduplicated warning codes describe
elements lost or denied while taking the snapshot. Names and IDs are capped at
512 characters; the helper never retrieves ValuePattern or TextPattern content.

An isolated `powershell.exe -Mta` process makes all UIA calls. If a provider blocks
inside COM, the parent attempts to terminate that worker and returns
`UIA_TIMEOUT`; an in-loop elapsed-time check alone would not stop a stuck provider.
The private `-UiWorker` switch is an implementation detail and must not be called
directly by clients, since it bypasses the parent timeout.

## Bounds and public errors

Requests accept at most 16,000 decoded bytes and 24,000 Base64 characters. List
output, tree depth, node count, and displayed text lengths have fixed caps.
Unknown actions return `UNKNOWN_ACTION`; malformed fields return
`INVALID_REQUEST`. Other errors include `UNSUPPORTED_PLATFORM`,
`WINDOW_NOT_FOUND`, `INTERACTIVE_SESSION_REQUIRED`, `UIA_UNAVAILABLE`, `UIA_TIMEOUT`, `UIA_WORKER_UNAVAILABLE`,
`UIA_WORKER_FAILED`, and `INTERNAL_ERROR`. Raw exception messages and credential
values are not included in errors.

`windows.list` and `ui.tree` reject session 0 and noninteractive contexts with
`INTERACTIVE_SESSION_REQUIRED`. Metadata also
reports `user`, `interactive` (also `user_interactive`), `desktop` (if obtainable), and `coordinate_space`
(`null` for process-only data). The desktop name describes the calling thread
and does not establish that it is the currently visible input desktop.

The metadata and UI strings can reveal application names, document titles,
labels, and requested executable paths. The calling host should show or retain
only the data needed for the user's requested task.

## Validation status and Windows smoke check

This implementation was authored on macOS. Windows native calls, UI Automation,
PowerShell execution, mixed-DPI behavior, Windows on Arm, and the interactive
session behavior of `prlctl exec` have **not** been runtime-verified here.

Before relying on this helper, run it directly in a Windows interactive session:

1. Start Notepad manually. Request `processes.list` with `name:"notepad"`; repeat
   with its `pid` and `include_path:true`.
2. Request `windows.list` for that PID. Check the title and bounds against the
   visible Notepad window. Move it to a second display with a different scale and
   repeat the rectangle check.
3. Pass that handle to `ui.tree`; look for the actual labels and enabled states
   exposed by that version of Notepad. Check `max_nodes:1` and `max_depth:0` produce
   only the root and the appropriate limit flags.
4. Close Notepad and retry the old handle; expect `WINDOW_NOT_FOUND` or
   `UIA_UNAVAILABLE` when it closes during inspection. Handles can be reused by
   Windows, so do not retain them as permanent application identifiers.
5. Check invalid Base64, an unknown action, and out-of-range limits produce one
   JSON error with exit code `1`. Confirm the caller handles `UIA_TIMEOUT` without
   retrying indefinitely.
6. Repeat the known-window check through the Parallels transport and compare
   `session_id`, `user`, and `desktop` with the interactive run. A successful process list alone is not
   evidence that UI inspection through that transport works.

## Microsoft API references

- [EnumWindows: top-level desktop window enumeration](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-enumwindows)
- [GetWindowRect: DPI virtualization and invisible resize borders](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getwindowrect)
- [UI Automation bounding rectangles: physical screen coordinates](https://learn.microsoft.com/en-us/dotnet/api/system.windows.automation.automationelement.boundingrectangleproperty)
- [UI Automation and screen scaling](https://learn.microsoft.com/en-us/windows/win32/winauto/uiauto-screenscaling)
- [UI Automation threading and MTA guidance](https://learn.microsoft.com/en-us/windows/win32/winauto/uiauto-threading)
- [UI Automation runtime IDs](https://learn.microsoft.com/en-us/dotnet/api/system.windows.automation.automationelement.getruntimeid)
- [UIPI and UIAccess security boundaries](https://learn.microsoft.com/en-us/windows/security/threat-protection/security-policy-settings/user-account-control-allow-uiaccess-applications-to-prompt-for-elevation-without-using-the-secure-desktop)
