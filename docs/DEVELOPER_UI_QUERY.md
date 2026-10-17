# Search Windows controls

Install the complete [developer helper](DEVELOPER.md), then use fresh
`guest processes` and `guest windows` observations to obtain the target HWND,
PID and process `start_time_utc`. These three fields are required together;
the worker checks the window owner and process start time before and after
inspection. UI queries require the logged-in Windows interactive session.

Save this synthetic example as a request file, replacing the three identity
fields with observations from your own test application:

```json
{
  "hwnd": "0x123",
  "pid": 42,
  "start_time_utc": "2026-01-02T03:04:05.1234567Z",
  "selector": {"control_type": "Button", "name": "Fixture button"},
  "ancestor": {"automation_id": "FixturePanel"},
  "mode": "list",
  "offset": 0,
  "limit": 50,
  "max_depth": 6,
  "max_scan_nodes": 2000,
  "timeout_ms": 10000,
  "include_value": false
}
```

```sh
./bin/starbridge dev ui.query VM --request query.json
```

## Selectors and budgets

`selector` and `ancestor` are optional objects with any of `name`,
`automation_id`, `control_type`, `class_name`, and `name_contains`. Exact fields
are case-sensitive; `name_contains` is an ordinal case-insensitive substring.
All supplied conditions must match. Names/class names are limited to 256 Unicode
code points, automation IDs to 512, and control types to 64. No regular expression
or script can be supplied. An omitted or empty selector matches every control.
An ancestor condition must match at least one ancestor between the control and
the requested window root; it does not match the control itself.

The window is the root of a depth-first UIA Control View search. Defaults and
ranges are `offset=0` (`0..4999`), `limit=50` (`1..200`), `max_depth=6` (`0..12`),
`max_scan_nodes=2000` (`1..5000`), and `timeout_ms=10000` (`1000..20000`).
The depth and scan budgets include the root at depth zero. A separate MTA worker
is terminated on timeout; no UI actions are performed by this query.

`mode:list` returns a page and its observed match count. `mode:one` defaults to
`limit:1`, requires `offset:0`, and reports `ELEMENT_NOT_FOUND`,
`AMBIGUOUS_MATCH`, or `QUERY_INCOMPLETE` instead of choosing a control arbitrarily.
Uniqueness applies to the configured window/depth scope, not the entire desktop.

## Result

The ordinary `dev-1` response contains:

- `window:{hwnd,pid,start_time_utc}`, `snapshot_id`, UTC `timestamp`,
  `coordinate_space:"guest-screen-physical-pixels"`, and `pagination:"live"`.
- The applied `mode`, `offset`, `limit`, `max_depth`, `max_scan_nodes` and
  `include_value`, followed by `scanned_nodes`, `matched_count`, `returned_count`,
  `items`, `has_more`, `complete`, `node_limit_reached`, `depth_limit_reached`
  and up to 16 warning codes.
- Each item has `node_id`, `parent_id`, `runtime_id`, `depth`, `pid`, `name`,
  `control_type`, `automation_id`, `class_name`, `framework_id`,
  `rect:{x,y,width,height}` (or `null`), `is_enabled`, `is_offscreen`,
  `is_password`, `patterns`, `state`, `value`, `value_status`, and
  `value_truncated`.

`patterns` lists supported UIA pattern names. `state` contains nullable `toggle`
(`Off`, `On`, `Indeterminate`), `is_selected`, `expand_collapse` (`Collapsed`,
`Expanded`, `PartiallyExpanded`, `LeafNode`), `value_read_only`, `range_value`,
`range_minimum`, and `range_maximum`. These fields describe what the provider
exposes; missing patterns produce null state fields.

Values are opt-in. With `include_value:true`, ValuePattern or TextPattern content
is returned up to 2048 code points. `value_status` is `not_requested`, `available`,
`unsupported`, `unavailable`, or `password_skipped`. Password controls never
return values or pattern-state contents, even with opt-in. ValuePattern providers
return a whole string before client truncation; output and time budgets do not
limit an external provider's allocation.

`complete` describes traversal within the configured depth. It is false when
the scan budget or inaccessible children prevented that traversal; an incomplete
`matched_count` is a lower bound. `has_more` refers only to matches already
observed, so false does not prove exhaustion when `complete` is false.
`depth_limit_reached` records reaching the requested depth, not proof of further
descendants. Increase the scan budget or narrow the scope when necessary.

Every page is a new live observation: there is no persistent pagination cursor.
UI changes may alter later offsets. IDs are observation-local, parent IDs may
refer to filtered-out controls, and pages are not a complete serialized tree.
Re-query before an action. A rectangle, enabled state, or non-offscreen flag does
not prove visibility or clickability; guest pixels are not macOS coordinates.

## Verification

`tests/windows/developer-ui-query-contract.ps1` validates parameters, selector and
ancestor matching, pagination, scan limits, opt-in/password behavior, native
compilation and worker timeout using synthetic objects and an isolated sleeping
worker. It does not inspect the user's desktop or start an application or VM.
Go tests validate request normalization, private copies, bounds, result identity,
password redaction and malformed guest responses.

Actual UIA providers, Windows on Arm, mixed DPI and the Parallels current-user
transport still require [desktop acceptance](DESKTOP_TESTING.md). These synthetic
contracts do not establish that every application's provider supports the query.
