# 查询 Windows 控件

`ui.query` 在指定窗口内搜索 UI Automation（UIA）控件，返回位置、支持的能力接口（pattern）、状态和可选的当前值。它是只读开发动作，要求目标所在的 Windows 交互会话。先安装完整的 [developer helper](DEVELOPER.md)。MCP 名称为 `dev.ui.query`，服务端需开启 `--developer-tools`；它不需要写授权，配置动作策略时仍需对应 grant。

从 `guest processes` 和 `guest windows` 获取新鲜的 `hwnd`、`pid`、`start_time_utc`。三者必须一起提供；worker 在查询前后核对窗口归属和进程启动时间，防止句柄或 PID 被复用。`start_time_utc` 需精确到 100 ns 或更粗，主机将其规范化为 UTC。

## 请求示例

将以下合成参数保存为 `query.json`。预演不访问 VM；实际查询前需换成测试应用的身份和控件条件。

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
./bin/starbridge --dry-run dev ui.query fixture-vm --request query.json
# 替换为当前测试应用参数后执行：
./bin/starbridge dev ui.query VM --request query.json
```

## 选择条件与预算

`selector` 和 `ancestor` 均为可选对象，支持 `name`、`name_contains`、`automation_id`、`control_type`、`class_name`。同一对象中的条件必须全部匹配；精确字段区分大小写，`name_contains` 使用不区分大小写的序号子串比较，不接受正则或脚本。`name`、`name_contains`、`class_name` 最多 256 个 Unicode 码点，`automation_id` 为 512，`control_type` 为 64。

省略或留空 `selector` 表示匹配全部控件。`ancestor` 要求从父节点到窗口根（含根）的某个祖先匹配，不包含控件自身。搜索使用 UIA Control View，从窗口根按深度优先顺序遍历。

- `mode`：默认 `list`；`one` 要求在配置的搜索范围内恰好匹配一个控件。
- `offset`：默认 `0`，范围 `0..4999`；`limit` 默认 `50`，范围 `1..200`。
- `max_depth`：默认 `6`，范围 `0..12`，窗口根深度为 `0`。
- `max_scan_nodes`：默认 `2000`，范围 `1..5000`，包含窗口根及不匹配的已扫描节点。
- `timeout_ms`：默认 `10000`，范围 `1000..20000`，由独立 MTA worker 的进程期限约束。
- `include_value`：默认 `false`，仅显式设为 `true` 才读取非密码值。

`mode:"one"` 默认 `limit:1`，必须使用 `offset:0`、`limit:1`。没有匹配、多个匹配或搜索不完整时分别返回 `ELEMENT_NOT_FOUND`、`AMBIGUOUS_MATCH`、`QUERY_INCOMPLETE`。唯一性只适用于指定窗口和深度范围。

## 输出与完整性

响应使用 [dev-1 envelope](DEVELOPER.md)，`data` 包含：

- `window:{hwnd,pid,start_time_utc}`、`snapshot_id`、UTC `timestamp`、`coordinate_space:"guest-screen-physical-pixels"` 和 `pagination:"live"`。
- 应用后的 `mode`、`offset`、`limit`、`max_depth`、`max_scan_nodes`、`include_value`。
- `scanned_nodes`、`matched_count`、`returned_count`、`items`、`has_more`、`complete`、`node_limit_reached`、`depth_limit_reached`，以及最多 16 个去重警告码。

每个 `items` 元素含 `node_id`、`parent_id`、`runtime_id`、`depth`、`pid`、`name`、`control_type`、`automation_id`、`class_name`、`framework_id`、`rect:{x,y,width,height}`（或 `null`）、`is_enabled`、`is_offscreen`、`is_password`、`patterns`、`state`、`value`、`value_status`、`value_truncated`。名称和 automation ID 最多 512 码点，class 为 256，framework 为 128。

`patterns` 列出提供程序支持的 pattern 名称，最多 32 项。`state` 的字段在不支持或不可读取时为 `null`：`toggle`（`Off`、`On`、`Indeterminate`）、`is_selected`、`expand_collapse`（`Collapsed`、`Expanded`、`PartiallyExpanded`、`LeafNode`）、`value_read_only`、`range_value`、`range_minimum`、`range_maximum`。

`include_value:true` 时优先读取 ValuePattern，再尝试 TextPattern，最多返回 2048 码点，截断不拆开有效代理对。`value_status` 为 `not_requested`、`available`、`unsupported`、`unavailable` 或 `password_skipped`。被提供程序标记为密码的控件不读取值或 pattern 状态；密码状态不可读取时也按密码处理。此保护依赖应用的 UIA 信息，不能保证隐藏应用通过其他标签暴露的内容。ValuePattern 会先返回完整字符串，再由 helper 截断；输出预算不限制外部提供程序自身的分配。

`complete` 表示配置深度内是否遍历完整。扫描预算耗尽、节点或子节点不可访问时为 `false`，此时 `matched_count` 只是下界。`has_more` 只描述已经观察到的匹配；当 `complete:false` 时，`has_more:false` 不代表没有更多控件。`depth_limit_reached` 记录到达配置深度，不证明该节点还有子节点。

每一页都是新的实时查询，没有持久分页游标。界面变化会改变后续 offset；`node_id` 仅在本次快照内有效，`parent_id` 可能指向被筛掉的节点，`runtime_id` 也不是永久身份。动作前应重新匹配，[ui.act](DEVELOPER_UI_ACTIONS.md) 会执行这一检查。

## 能力边界与验证

查询结果取决于应用的 UIA 提供程序。自绘界面、游戏或远程画布可能只暴露外层窗口；成功返回一个空页不等于应用没有可操作内容。有效矩形、`is_enabled:true` 或 `is_offscreen:false` 均不证明控件未被遮挡或可以点击。UIA 矩形为 Windows guest 的物理屏幕像素，不是 macOS 或 Parallels 查看器坐标。

需要视觉兜底时使用 [ui.observe / ui.click](DEVELOPER_VISUAL.md)。桥接层本身不提供 OCR 或视觉模型。

`tests/windows/developer-ui-query-contract.ps1` 使用合成控件与隔离的等待 worker，覆盖选择条件、祖先匹配、分页、预算、密码保护、文本截断、原生编译及超时，不检查真实桌面。Go 测试覆盖参数、响应、身份及预算。实际 UIA 提供程序、Windows on Arm、混合 DPI 和 Parallels 当前用户传输仍需[桌面验收](DESKTOP_TESTING.md)。
