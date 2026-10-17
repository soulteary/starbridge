# Operate an identified test window

These developer actions require `--allow-write`, including dry runs. Use a
dedicated interactive Windows test session and install the complete developer
helper tree. Every request includes fresh `hwnd`, `pid`, and `start_time_utc` as
described by [UI queries](DEVELOPER_UI_QUERY.md).

```sh
./bin/starbridge --dry-run dev ui.act VM --request invoke.json --allow-write
./bin/starbridge dev ui.act VM --request invoke.json --allow-write
```

```json
{
  "hwnd": "0x123",
  "pid": 42,
  "start_time_utc": "2026-01-02T03:04:05.1234567Z",
  "operation": "invoke",
  "selector": {"name": "Fixture button", "control_type": "Button"},
  "max_depth": 6,
  "max_scan_nodes": 2000,
  "timeout_ms": 10000,
  "lock_timeout_ms": 0
}
```

## Semantic operations

`ui.act` performs one `invoke`, `set_value`, `toggle`, `select`, `expand`,
`collapse`, `scroll`, `scroll_into_view`, or `focus` operation. `selector` is
required and nonempty; `ancestor`, depth and scan budgets follow `ui.query`.
Each invocation searches again in unique-match mode before obtaining the
current UIA pattern. Empty, ambiguous, stale or incompletely searched scopes
are rejected. Optional `expected_runtime_id` is an additional freshness guard,
not a permanent selector; `node_id` and old snapshot IDs are not accepted.

`set_value` requires `value` (at most 4096 code points, including an empty string)
and rejects read-only providers. It never reads or returns the supplied value,
including for password controls. All values are redacted in previews.
`scroll` requires a nonzero `horizontal` or `vertical` amount: `none`,
`small_increment`, `small_decrement`, `large_increment`, or `large_decrement`.
`toggle` performs one toggle cycle, with no automatic cycle to a desired state.
Disabled controls are rejected; offscreen controls only permit `scroll_into_view`.

## Window and input actions

- `window.focus` requests normal Windows foreground activation, restoring a
  minimized window if needed. It does not bypass Windows foreground restrictions.
- `window.resize` requires `width` and `height` (`64..8192`); optional `x`/`y`
  (`-65535..65535`) otherwise retain the current position. It reports the actual
  `GetWindowRect` rectangle in physical guest pixels; application constraints
  may adjust the requested size. It does not activate or reorder the window.
- `input.key` sends one named `key`: `A..Z`, `0..9`, `F1..F12`, `Enter`, `Tab`,
  `Escape`, `Space`, `Backspace`, `Delete`, `Insert`, `Home`, `End`, `PageUp`,
  `PageDown`, `Left`, `Right`, `Up`, or `Down`. Optional `modifiers` contains
  distinct `Control`, `Alt`, and/or `Shift`. It provides no freeform text or
  Windows-key interface; prefer `set_value` for a UIA value, or `input.text`
  for literal Unicode input. All key input is redacted
  in previews and omitted from results.
- `input.text` inserts required `text` into the target's currently focused
  control, without requiring UIA ValuePattern. See the Unicode contract below.
- `input.mouse` requires physical guest-screen `x`/`y` (`-65535..65535`).
  `event` is `click` (default), `move`, or `wheel`; clicks accept `button`
  `left` (default), `right`, or `middle`. Wheel input requires `wheel_delta`,
  a nonzero multiple of 120 in `-1200..1200`. The point must hit the target's
  uncovered client area. No drag, double-click, screen-wide input or coordinate
  conversion from the macOS viewer is implied.

Keyboard/mouse input requires the exact target to be foreground first; it never
silently focuses another window. A held modifier or mouse button causes
`INPUT_BUSY`. Physical-coordinate operations require a per-monitor DPI context.
All writes require a visible, enabled window on the caller's accessible input
desktop and current session. Locked/secure desktops, minimized windows (except
for explicit focus), wrong identities, and inaccessible desktops are rejected.

## Literal Unicode text

```sh
./bin/starbridge --dry-run dev input.text VM --request text.json --allow-write
./bin/starbridge dev input.text VM --request text.json --allow-write
```

```json
{
  "hwnd": "0x123",
  "pid": 42,
  "start_time_utc": "2026-01-02T03:04:05.1234567Z",
  "text": "中文 😀",
  "timeout_ms": 10000,
  "lock_timeout_ms": 0
}
```

`text` contains `1..2048` valid Unicode code points, without NUL. Empty text,
invalid UTF-8 and unpaired surrogate escapes/units are rejected; an actual
U+FFFD replacement character is valid. The common request budget remains
16000 UTF-8 JSON bytes. No clipboard, IME composition or keyboard-layout
conversion is used; existing `ui.act` `set_value` remains available and is
preferable when an application exposes a writable UIA value.

The newline policy is **literal**: CR, LF, CRLF and Tab are preserved exactly.
They are sent as Unicode characters, never converted to `VK_RETURN` or
`VK_TAB`. Applications may ignore them or interpret control characters as
editing, submission or other actions. In particular LF alone is not guaranteed
to create a newline in native Windows Edit controls. Use explicit `input.key`
`Enter`/`Tab` when the intended operation is a key press, and test the target
application's behavior before sending a multiline value. No printable text is
silently normalized.

The worker validates the same window/session/input-desktop identity and named
session mutex as other input actions, then checks foreground and that no
keyboard key or mouse button is held. The currently focused control receives
the input; a window identity does not identify that child control. Set or check
focus explicitly before dispatch. There is no implicit focus change or UIPI
bypass, and applications that do not process Unicode `VK_PACKET` input may
ignore it. DPI conversion is unnecessary because this action has no coordinates.

Each UTF-16 code unit becomes a `KEYEVENTF_UNICODE` down/up pair with virtual
key zero. Supplementary characters retain their high/low surrogate order.
At most 4096 UTF-16 units / 8192 events are inserted by one `SendInput` call.
Success adds `code_point_count`, `utf16_unit_count`, `input_count` and
`newline_policy:"literal"`; counts describe the original unchanged text.
It also reports `foreground_after`, which may already have changed while the
application processes input. These counts confirm insertion, not acceptance
or completion by the application.

Partial, denied or interrupted insertion has `outcome:"unknown"` and is never
retried automatically. A partial batch only attempts the unmatched final
Unicode key-up, if there is one; it does not send missing characters or finish
a surrogate pair. Inspect application state before another write. Input text
is redacted in CLI/MCP dry runs and omitted from results and errors, including
raw transport/provider diagnostics; identity, timing and counts remain visible.
Request files and process arguments still contain the text while executing,
and subsequent UI queries or screenshots may show what the application displays.

Microsoft documents the Unicode flags and foreground-message behavior in
[KEYBDINPUT](https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-keybdinput),
and insertion counts, keyboard-state effects and integrity restrictions in
[SendInput](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput).

## Coordination and outcomes

Workers serialize dispatch using a named mutex scoped to the Windows session,
including independent CLI processes. `lock_timeout_ms` defaults to `0` (fail
immediately with `SESSION_BUSY`), range `0..5000`, and must be shorter than
`timeout_ms`. The worker budget defaults to 10000 ms, range `1000..20000`.

The mutex coordinates Starbridge workers; it does not lock out humans or other
automation, or wait for an application's asynchronous work. Foreground and
mouse hit checks reduce mistargeting but cannot make the entire desktop atomic.
Avoid concurrent human input in the test session.

Success returns `action`, the verified `window` identity, UTC `timestamp`,
`coordination:"windows-session-mutex"`, and `outcome:"dispatched"`.
`window.focus` may instead report `already_satisfied`. Semantic results include
the operation and the element's runtime ID, control type and password flag;
resize results include actual bounds; key/mouse results include the inserted
event count and subsequent foreground handle. No supplied text or key content
is returned. A dispatched pattern/event is not proof that the application has
finished processing it: check an explicit postcondition before the next write.

There is no automatic action retry. Timeout, partial input, provider failure or
worker loss after dispatch may leave an unknown outcome; inspect application
state before deciding what to do next. A partial input insertion only triggers
best-effort release of this operation's keys/buttons, never a repeated click.

## Verification

`tests/windows/developer-ui-action-contract.ps1` uses synthetic UIA objects,
native structure/coordinate checks and a disposable cross-process mutex holder.
It does not operate a real window or send input. Go tests check authorization,
request budgets, input redaction, identity and result validation. Actual UIA
patterns, focus policy, foreground changes, DPI and input insertion require
opt-in acceptance against a disposable test fixture.

`tests/windows/developer-input-text-contract.ps1` additionally checks Chinese,
emoji surrogate pairs, literal line endings, event/code-point bounds and
partial-insertion cleanup through synthetic delegates. It never calls
`SendInput`. Go tests cover raw JSON Unicode, CLI/MCP registration, independent
write authorization, previews, result counts and private failure envelopes.
