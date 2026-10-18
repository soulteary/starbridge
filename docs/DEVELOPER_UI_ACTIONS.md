# 操作 Windows 测试应用

以下动作均为写操作，CLI 即使预演也必须显式加 `--allow-write`；MCP 另需服务端 `--developer-tools`、`--allow-guest-write` 和对应动作授权。安装完整的 [developer helper](DEVELOPER.md)，在专用 Windows 交互测试会话中使用。

每次请求都需新鲜的 `hwnd`、`pid`、`start_time_utc`，获取方法见[控件查询](DEVELOPER_UI_QUERY.md)。窗口身份不能代替子控件身份，也不能阻止应用异步改变内容。

## UIA 语义操作

`ui.act` 支持 `invoke`、`set_value`、`toggle`、`select`、`expand`、`collapse`、`scroll`、`scroll_into_view`、`focus`。保存以下合成参数为 `invoke.json`：

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

```sh
./bin/starbridge --dry-run dev ui.act fixture-vm --request invoke.json --allow-write
# 替换为当前测试应用的身份和条件后执行：
./bin/starbridge dev ui.act VM --request invoke.json --allow-write
```

`selector` 必须非空；其字段、可选 `ancestor`、深度和扫描预算沿用 `ui.query`。每次操作前重新搜索，只有完整搜索范围内唯一匹配才继续；不会接受旧 `node_id`。可选 `expected_runtime_id` 是额外的新鲜度校验，不是永久选择器。应用必须支持对应 UIA pattern；缺少 pattern、控件禁用或不可访问时拒绝执行。离屏控件只允许 `scroll_into_view`。

- `set_value` 需 `value`，最多 4096 码点，可为空；拒绝只读 提供程序。不读取或回显写入值，密码控件也适用；预演中隐藏值。
- `scroll` 需至少一个非零的 `horizontal` 或 `vertical`：`none`、`small_decrement`、`small_increment`、`large_decrement`、`large_increment`。
- `toggle` 只切换一次，不自动循环到某个目标状态。

## 窗口、按键与鼠标

这些动作也使用上述三个窗口身份字段：

- `window.focus`：请求普通前台激活，必要时恢复最小化窗口；保留 Windows 的前台激活限制。
- `window.resize`：需 `width`、`height`（`64..8192`）；可选 `x`、`y`（`-65535..65535`），省略时保留当前位置。使用物理像素的 `GetWindowRect`，返回实际矩形；应用约束可能调整大小，不主动激活或改变层叠顺序。
- `input.key`：一个 `key`，支持 `A..Z`、`0..9`、`F1..F12`、`Enter`、`Tab`、`Escape`、`Space`、`Backspace`、`Delete`、`Insert`、`Home`、`End`、`PageUp`、`PageDown`、`Left`、`Right`、`Up`、`Down`。可选 `modifiers` 为不重复的 `Control`、`Alt`、`Shift`，不提供 Windows 键接口。预演隐藏按键，结果不含按键内容。
- `input.mouse`：需物理屏幕 `x`、`y`（`-65535..65535`）；`event` 为 `click`（默认）、`move` 或 `wheel`。点击的 `button` 为 `left`（默认）、`right` 或 `middle`；滚轮需 `wheel_delta`，为 `-1200..1200` 中非零的 120 倍数。目标点必须命中指定窗口未被遮挡的客户区；不提供拖动或双击。
- `input.text`：向当前焦点控件发送 Unicode 文本，详见下一节。

键鼠输入要求指定窗口已经在前台，不会隐式切换焦点。已按下修饰键或鼠标按钮时返回 `INPUT_BUSY`；`input.text` 还检查其他键盘按键。所有写操作要求目标与调用者处于同一可访问输入桌面和会话，窗口可见且启用。最小化窗口需先显式恢复/聚焦。锁屏、安全桌面、身份不匹配和不可访问桌面会被拒绝；不绕过 UIPI 或提升权限。鼠标坐标和窗口调整要求 per-monitor DPI 上下文；坐标不能直接取自 macOS 查看器。

## Unicode 文本输入

保存为 `text.json`，可先离线预演：

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

```sh
./bin/starbridge --dry-run dev input.text fixture-vm --request text.json --allow-write
# 替换身份并确认子控件焦点后执行：
./bin/starbridge dev input.text VM --request text.json --allow-write
```

`text` 必须为 `1..2048` 个有效 Unicode 码点，不含 NUL。空文本、无效 UTF-8 和孤立代理项被拒绝；实际的 U+FFFD 字符合法。CLI/MCP 参数和工作流原始 JSON 在解码前校验，避免静默替换无效字符；Windows 在序列化前、worker 内和原生构造时也校验。整个开发请求仍不得超过 16000 字节 UTF-8 JSON。

换行策略固定为 **literal**：CR、LF、CRLF、Tab 原样保留，不规范化，也不合成为 `VK_RETURN` / `VK_TAB`。应用可能忽略这些字符，或将它们解释为换行、编辑、提交等操作；尤其不能保证 LF 在原生 Edit 控件中产生换行。需要真正的 Enter/Tab 按键时使用 `input.key`，多行输入应先确认应用行为。

每个 UTF-16 码元生成一组 `KEYEVENTF_UNICODE` 按下/抬起事件，虚拟键为零，emoji 等补充字符保持高/低代理项顺序。一次 `SendInput` 最多发送 4096 码元、8192 事件。不使用剪贴板、IME 组合或键盘布局转换，也不要求 UIA ValuePattern；应用仍需处理 Unicode `VK_PACKET` 输入。若有可写的 UIA value，可继续使用 `ui.act` 的 `set_value`。

文本发往目标窗口内**当前焦点控件**，调用者应先设置或检查该焦点。此动作没有坐标，不需要 DPI 换算。成功返回 `code_point_count`、`utf16_unit_count`、`input_count`、`newline_policy:"literal"` 和 `foreground_after`；计数对应原样文本，只确认事件插入，不证明应用接受或完成处理。

部分发送只尽力补上末尾未配对的 Unicode key-up，不补发字符、不补全代理对、不重发整段。无法确认结果时返回 `error.details.outcome:"unknown"`。预演、结果和错误不回显文本，包括原始传输/提供程序 诊断；身份、时间和计数仍可见。执行期间请求文件与进程参数含有文本，之后的 UI 查询或截图也可能显示应用内容。

## 协调、结果与失败

同一 Windows 会话中的 Starbridge worker 使用命名互斥锁串行派发，包括不同 CLI 进程。`lock_timeout_ms` 默认 `0`（遇占用立即返回 `SESSION_BUSY`），范围 `0..5000`，必须小于 `timeout_ms`；后者默认 `10000`，范围 `1000..20000`。锁被异常遗弃时返回 `SESSION_RECOVERY_REQUIRED`，应先检查应用状态。

互斥锁不阻止人工输入或其他自动化，也不等待应用完成异步工作。成功包含 `action`、验证过的 `window`、UTC `timestamp`、`coordination:"windows-session-mutex"`、`outcome:"dispatched"`；`window.focus` 已满足时可返回 `already_satisfied`。UIA 结果含 operation、runtime ID、控件类型和密码标记；resize 返回实际矩形；键鼠输入返回事件数和随后前台句柄，不返回输入内容。

所有动作都不自动重试。worker 能确认尚未开始时可返回 `not_started`；参数或授权错误也可能在发送请求前直接返回。超时、部分发送、派发后的提供程序失败或 worker 丢失可能已经改变应用状态，`unknown` 表示需先观察再决定下一步。成功派发同样需通过 [等待/断言](DEVELOPER_WAIT.md)、新查询或截图验证业务结果。

## 验证与参考

`tests/windows/developer-ui-action-contract.ps1` 使用合成 UIA 控件、原生结构检查和一次性跨进程锁夹具。`tests/windows/developer-input-text-contract.ps1` 使用合成 C# recorder 验证中文、emoji、换行、事件预算、部分发送清理及异常处理，不调用真实 `SendInput`。Go 测试覆盖授权、原始 JSON Unicode、预演脱敏、身份与响应预算。真实 提供程序、焦点、DPI、键鼠和 Parallels 传输需另做[开发验收](DEVELOPER_ACCEPTANCE.md)。

原生行为参见 Microsoft 的 [KEYBDINPUT](https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-keybdinput) 与 [SendInput](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput)。
