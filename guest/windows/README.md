# Windows helper 协议

Windows 端有两个独立入口：

- `desktop-bridge.ps1`：供 `starbridge guest` 使用，只读列举进程、顶层窗口和 UIA 控件树，协议版本为 `"1"`。本页主要描述此协议。
- `developer-bridge.ps1` 与相邻 `developer/`：供 `starbridge dev` 使用，协议版本为 `"dev-1"`，提供[控件查询](../../docs/DEVELOPER_UI_QUERY.md)、[窗口与输入操作](../../docs/DEVELOPER_UI_ACTIONS.md)、[截图](../../docs/DEVELOPER_SCREENSHOTS.md)、[视觉观察/点击](../../docs/DEVELOPER_VISUAL.md)和其他注册开发动作。写操作需要独立授权，详见[开发协议](../../docs/DEVELOPER.md)。

helper 不监听网络端口。主机通过 Parallels 当前用户传输调用 Windows PowerShell；请求不能指定任意脚本或绕过桌面权限。进程/窗口/UIA 查询不启动应用或提升权限。

## 安装与运行会话

环境和宿主 CLI 配置见[安装与使用](../../docs/USAGE.md)。Windows 需 PowerShell 5.1 和 .NET Framework UI Automation 程序集；入口也可由 Windows 上的 PowerShell 7 调用，隔离 UIA worker 仍使用系统 Windows PowerShell。

开发入口必须与完整 `developer/` 树一起安装，保留嵌套 workers 和 `.cs` 文件。在 Windows 源码根目录首次手工复制开发 helper 的示例：

```powershell
New-Item -ItemType Directory -Path 'C:\ProgramData\Starbridge' -Force | Out-Null
Copy-Item .\guest\windows\developer-bridge.ps1 'C:\ProgramData\Starbridge\'
Copy-Item .\guest\windows\developer 'C:\ProgramData\Starbridge\' -Recurse -Force
```

运行 `starbridge dev system.info VM` 检查已安装动作。升级时需更换完整配套版本；仅覆盖旧目录可能残留已删除的模块，仍会被自动加载。受管安装及模块说明见[开发工具](../../docs/DEVELOPER.md)。

`windows.list` 和 `ui.tree` 要求已登录用户的交互会话，Session 0 或非交互环境返回 `INTERACTIVE_SESSION_REQUIRED`。进程查询成功、session metadata 或 desktop 名称都不证明能访问预期可见桌面；应通过实际 Parallels 传输核对一个已知窗口。锁屏、安全 UAC 桌面、较高权限应用及缺失或不完整的 UIA提供程序都可能限制查询。helper 不修改 UAC、UIAccess 或桌面安全设置。

## 请求与响应

`-RequestBase64` 接收 UTF-8 JSON 对象的 Base64 编码，`action` 必填，`params` 默认为 `{}`。Base64 不是加密。以下只读示例可在 Windows 执行：

```powershell
$request = @{ action = 'windows.list'; params = @{ limit = 100 } } | ConvertTo-Json -Compress
$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($request))
& powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass `
  -File 'C:\ProgramData\Starbridge\desktop-bridge.ps1' -RequestBase64 $encoded
```

`ExecutionPolicy Bypass` 只影响该进程，组织策略仍可能阻止执行。解码后请求最多 16000 字节，Base64 最多 24000 字符。

主机接受 `12.0`、`1.2e1` 等数学上精确为整数的 JSON 数字，再发送普通整数 `12` 和显式默认值。旧 helper 直接调用只接受整数 token，不接受小数、指数或数字字符串；直连调用者需先规范化。名称筛选与托管文本限制按 Unicode 码点计长，emoji 计一个，截断保留完整代理对；Win32 title/class 缓冲区则按 UTF-16 码元计长。

完成的调用在 stdout 输出一行紧凑 UTF-8 JSON。成功退出码为 `0`，helper 错误为 `1`。以下身份、用户名和时间均为合成数据：

```json
{"ok":true,"data":{"processes":[],"truncated":false,"limit":500},"meta":{"protocol_version":"1","action":"processes.list","timestamp":"2026-10-18T00:00:00.0000000Z","duration_ms":100,"user":"EXAMPLE\\User","session_id":1,"interactive":true,"user_interactive":true,"desktop":null,"coordinate_space":null,"read_only":true}}
```

```json
{"ok":false,"error":{"code":"WINDOW_NOT_FOUND","message":"No window with this hwnd is available in the current session."}}
```

成功 `meta` 包含 `protocol_version:"1"`、`action`、UTC `timestamp`、`duration_ms`、`user`、`session_id`、等价的 `interactive` / `user_interactive`、`desktop`（字符串或 `null`）、`coordinate_space`、`read_only:true`。desktop 名称属于调用线程，不必然是可见输入桌面；进程查询的坐标为空。UIA metadata 来自 worker，duration 不含外层进程启动。

主机拒绝同时含 `data` 和 `error`、缺少必需字段或类型不符的 envelope。失败至少需非空字符串 `error.code`、`error.message`；成功的 `data` 必须匹配动作，返回数量、深度和声明预算不得超过本次规范化请求。响应允许兼容扩展字段。PowerShell 启动或参数绑定失败可能发生在 helper 执行前，因此也要处理非零退出但没有 JSON 的情况。

## `processes.list`

```json
{"action":"processes.list","params":{"limit":500,"name":"notepad","include_path":false}}
```

`limit` 默认 `500`，范围 `1..1000`。可选 `pid` 为正 Windows PID（主机范围 `1..2147483647`）；旧 helper 直连另接受 `0` 表示不筛选，跨接口均建议省略该字段来列出全部进程。`name` 为最多 256 码点的不区分大小写子串；两个筛选同时提供时都需匹配。`include_path` 默认 `false`。

`data` 含按 PID 排序的 `processes`、布尔 `truncated`、整数 `limit`。指定 PID 已退出或不存在时返回空数组。每项含：

- `pid`、`name`（最多 512 码点）。
- `session_id`、`start_time_utc`，不可读取时为 `null`；两者均读到时 `identity_status:"available"`，否则为 `partial`。
- `path`，仅请求且可读取时返回，最多 2048 码点，否则为 `null`；`path_status` 为 `not_requested`、`available` 或 `unavailable`。

`truncated:true` 表示省略了额外匹配进程。不读取命令行或环境变量。启动时间可辅助识别 PID 复用，结果仍是会发生竞争的瞬时观察。

## `windows.list`

```json
{"action":"windows.list","params":{"limit":500,"include_hidden":false}}
```

`limit` 和 `pid` 沿用进程查询规则。`include_hidden` 默认 `false`，仅返回 Win32 visibility 标记为真的窗口；这不证明窗口未被遮挡、已绘制或位于当前虚拟桌面。

`data` 含 `windows`、`truncated`、`limit`、`dpi_awareness`、`coordinate_space`、`rect_source:"GetWindowRect"`。每个窗口含 `hwnd`、`pid`、`process_name`（可能为 `null`）、`title`、`class_name`、`is_visible`、`rect:{x,y,width,height}`。HWND 使用 `"0x30A52"` 这样的字符串；title/class 缓冲区分别最多 512 / 256 个 UTF-16 码元。

枚举对象是桌面应用顶层窗口，不含子控件或全部应用表面。`GetWindowRect` 可能包括不可见调整大小边距。建立 per-monitor DPI 上下文时坐标为 `guest-screen-physical-pixels`，否则为 `guest-screen-dpi-context-dependent-pixels`。左侧或上方显示器可以有负坐标。这些均为 Windows guest 坐标，不是 macOS 或 Parallels 查看器坐标。

## `ui.tree`

先从新鲜的 `windows.list` 结果取得 HWND：

```json
{"action":"ui.tree","params":{"hwnd":"0x30A52","max_depth":6,"max_nodes":300,"timeout_ms":10000}}
```

- `hwnd` 必填。主机接受 ASCII 十进制或 `0x` / `0X` 十六进制字符串，范围 `1..0x7fffffffffffffff`，允许前导零，再发送无前导零的小写十六进制，例如 `000000000000000000001` 变为 `0x1`。旧 helper 直连最多 20 位十进制或 16 位十六进制；句柄还必须适合 Windows 进程指针位数。
- `max_depth` 默认 `6`，范围 `0..12`；根深度为 `0`。
- `max_nodes` 默认 `300`，范围 `1..1000`，包含根节点。
- `timeout_ms` 默认 `10000`，范围 `1000..20000`，约束 worker 启动和查询。超时返回 `UIA_TIMEOUT` 并尝试终止 worker。直连客户端不要使用绕过外层期限的私有 `-UiWorker` 开关。

`data` 含 `hwnd`、`snapshot_id`、`view:"control"`、`nodes`、`warnings`、`max_nodes`、`max_depth`、`node_limit_reached`、`depth_limit_reached`、`coordinate_space:"guest-screen-physical-pixels"`、`dpi_awareness`。`nodes` 是从指定根开始、深度优先的扁平 Control View 树：

```json
{"node_id":"n000002","parent_id":"n000001","depth":1,"runtime_id":[42,1234,7],"name":"Save","control_type":"Button","automation_id":"SaveButton","class_name":"Button","framework_id":"Win32","pid":1200,"rect":{"x":480,"y":320,"width":90,"height":30},"is_enabled":true,"is_offscreen":false,"is_password":false}
```

`node_id` 只在本次 `snapshot_id` 内唯一，根的 `parent_id` 为 `null`；`runtime_id` 是不透明整数数组或 `null`，不是持久选择器。`automation_id` 可以为空或重复。name / automation ID 最多 512 码点，class 为 256，framework 为 128。HWND、PID、UIA ID 都可能消失或复用，应重新查询。

`node_limit_reached` 表示额外节点被省略。`depth_limit_reached` 表示有返回节点到达配置深度，不证明还有子节点。最多 16 个去重警告码为 `runtime_id_unavailable`、`element_unavailable_or_access_denied`、`children_unavailable_or_access_denied`、`siblings_unavailable_or_access_denied`。

UIA 矩形是物理屏幕像素，空或不可用矩形为 `null`；非离屏标记与有效矩形不证明可点击。自绘应用、游戏、远程画布或实现不完整的提供程序可能只暴露外层窗口。这个旧入口不读取 ValuePattern / TextPattern 值，也不提供 OCR；需要选择器、状态和可选值时使用[开发查询](../../docs/DEVELOPER_UI_QUERY.md)。

## 错误与验证

稳定 helper 错误包括 `UNKNOWN_ACTION`、`INVALID_REQUEST`、`UNSUPPORTED_PLATFORM`、`INTERACTIVE_SESSION_REQUIRED`、`WINDOW_NOT_FOUND`、`UIA_UNAVAILABLE`、`UIA_TIMEOUT`、`UIA_WORKER_UNAVAILABLE`、`UIA_WORKER_FAILED`、`INTERNAL_ERROR`，不回显原始异常。成功 metadata 和界面内容仍可能含用户名、窗口/文档标题、标签或显式请求的可执行路径。

自动检查见 `tests/windows/guest-smoke.ps1` 和 `parameter-contract.ps1`；真实窗口/UIA 校验见[基础验收](../../docs/ACCEPTANCE.md)与[自有桌面夹具](../../docs/DESKTOP_TESTING.md)。开发动作另见[开发验收](../../docs/DEVELOPER_ACCEPTANCE.md)。

性能采样可运行 [`guest-performance.ps1`](../../tests/windows/guest-performance.ps1)：`-Hwnd` 使用新鲜窗口句柄，按节点上限 `1,25,100,250,1000` 采样 UIA，`-OutputPath` 保存 CSV。`wall_ms` 包含进程启动和输出，`helper_ms` 来自 metadata；应比较相同窗口和环境。脚本不启动 VM/应用，也不预设加速结论。

原生接口参见 Microsoft 的 [EnumWindows](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-enumwindows)、[GetWindowRect](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getwindowrect)、[UIA 物理坐标](https://learn.microsoft.com/en-us/dotnet/api/system.windows.automation.automationelement.boundingrectangleproperty)、[UIA runtime ID](https://learn.microsoft.com/en-us/dotnet/api/system.windows.automation.automationelement.getruntimeid) 与 [UIPI / UIAccess](https://learn.microsoft.com/en-us/windows/security/threat-protection/security-policy-settings/user-account-control-allow-uiaccess-applications-to-prompt-for-elevation-without-using-the-secure-desktop)。
