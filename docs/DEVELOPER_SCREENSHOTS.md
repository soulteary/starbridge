# 窗口截图

`screenshot.capture` 是只读交互开发动作，向指定顶层窗口请求一张有界 PNG，不主动激活或恢复窗口。先安装完整的 [developer helper](../guest/windows/README.md)，包括 workers 下的 `.ps1` 和 `screenshot-wgc.cs`。CLI 返回 JSON 中的 Base64 图片；MCP 服务端需开启 `--developer-tools`，无需 `--allow-guest-write`，返回原生 `image/png`，文字和 structured content 保留元数据，不重复 Base64。宿主证据落盘见[证据导出](DEVELOPER_EVIDENCE.md)。

## 先探测，再选择后端

```sh
./bin/starbridge dev screenshot.capabilities VM
```

`screenshot.capabilities` 不选择窗口或截图，只探测 OS、API 和硬件设备。它只接受可选 `timeout_ms`，默认 `10000`，范围 `1000..20000`；CLI 和 MCP 均可调用，不需写授权，可在 Session 0 运行。

结果含 `probe_scope:"os-api-device"`、`os_build`、`session_id`、`user_interactive`、`capture_requires_input_desktop:true`，以及两个 `methods` 项，各含 `capture_method`、`available`、安全的 `reason`。`print_window` 标记为可用；WGC 可能报告系统、API、策略或设备不支持。这里的可用性不代表交互会话已就绪，也不保证某个窗口允许捕获或能提供正确画面。

`capture_method` 可选：

- `print_window`：默认。调用应用提供的 `PrintWindow(hwnd, ..., 0)` 全窗口渲染。
- `windows_graphics_capture`：显式请求 WGC 合成器帧，要求 Windows 10 1903 / build 18362 或更高版本、相关 API 和硬件 D3D11 设备可用。

不自动选后端；一个后端失败不会重试另一个，也不会退回整屏或桌面区域捕获。

## 请求与预算

以下为合成 `capture.json`，可直接预演。实际截图前应换成测试应用的新鲜身份，获取方法见[控件查询](DEVELOPER_UI_QUERY.md)。

```json
{
  "hwnd": "0x123",
  "pid": 42,
  "start_time_utc": "2026-01-02T03:04:05.1234567Z",
  "capture_method": "print_window",
  "timeout_ms": 10000,
  "max_pixels": 8388608,
  "max_bytes": 4194304
}
```

```sh
./bin/starbridge --dry-run dev screenshot.capture fixture-vm --request capture.json
# 替换身份后执行；选择 WGC 时修改 capture_method：
./bin/starbridge dev screenshot.capture VM --request capture.json
```

`hwnd`、`pid`、`start_time_utc` 必须一起提供，helper 在捕获前后核对身份。调用者和目标必须处于同一 Windows 会话及可访问输入桌面。Session 0、锁屏或不可访问桌面、隐藏或最小化窗口会被拒绝；禁用窗口仍可截图。

- `timeout_ms`：默认 `10000`，范围 `1000..20000`，外层 worker 期限也约束阻塞原生调用。
- `max_pixels`：默认 `8388608`，范围 `1..16777216`；单边上限 `16384`。
- `max_bytes`：默认 `4194304`，范围 `1024..4194304`，限制编码后的 PNG。

未知字段、宿主路径和越界预算在传输前拒绝。分配前检查尺寸，编码流在写入时限制字节数；不自动缩放或重试。WGC 的完整帧池、有效内容和最终图像均需满足像素预算，裁掉边距不能绕过分配限制。

## 输出与坐标

`data` 包含：

- 目标 `hwnd`、`pid`、`start_time_utc`。
- UTC `capture_started_at` 和 `timestamp`，分别为渲染/编码前后时间，不代表整个应用的原子快照。
- `rect:{x,y,width,height}`、`width`、`height`、`coordinate_space:"guest-screen-physical-pixels"`。PNG 左上角 `(0,0)` 对应 `rect` 的 `(x,y)`，支持负坐标显示器。
- `png_base64`、`byte_length`、`sha256`：原始 PNG、压缩字节数及这些字节的小写 SHA-256。主机核对完整 PNG 解码、哈希、尺寸、身份和本次预算。
- 实际 `capture_method` 与 `warnings`。PrintWindow 返回 `application-rendered-best-effort`；WGC 返回 `compositor-frame-best-effort`、`bgra8-hdr-may-clip`、`system-capture-border-may-appear`。

两种后端的捕获边界不同，不能互换同一张图的坐标：

- PrintWindow 使用 per-monitor DPI 上下文中的 `GetWindowRect`，可能包含不可见的调整大小边距。
- WGC 返回 `DWMWA_EXTENDED_FRAME_BOUNDS` 合成器边界，排除这些边距。帧池按 `GraphicsCaptureItem.Size` 分配，只读取帧的有效 `ContentSize`，不编码未定义的纹理填充。
- 有效内容与 DWM 尺寸相同时直接使用；与物理 `GetWindowRect` 相同时，按实际矩形偏移裁掉边距，包含不对称边距。其他不能确定原点/尺寸的组合返回 `WGC_GEOMETRY_UNAVAILABLE`，不猜坐标。检测到捕获期间窗口移动、大小或 item 尺寸变化时返回 `WINDOW_GEOMETRY_CHANGED`。

这些都是 Windows guest 物理坐标，不能直接使用 macOS 或 Parallels 查看器坐标。无需额外乘一个 DPI 缩放系数。

## 后端限制与错误

PrintWindow 是同步的应用渲染请求，可能阻塞。部分 GPU 或自绘应用即使 API 成功，也可能返回空白、局部或旧画面；有效 PNG 不证明视觉内容正确。它不复制被遮挡窗口上方其他应用的桌面像素，但目标应用仍可能无法完整渲染。

WGC 使用 `CreateForWindow` 和 `CreateFreeThreaded`，不需要 picker、DispatcherQueue、WinMD、NuGet、额外 SDK 或编译器；随项目提供的 C# 由 Windows PowerShell 5.1 的 .NET Framework 编译器加载。生产路径要求硬件 D3D11；WARP 仅用于合成资源测试。帧经 BGRA8 读回后编码，HDR 颜色可能裁切。保留系统捕获指示，不请求去边框权限，也不绕过窗口的 display-affinity 捕获限制。WGC 同样不保证任意应用都返回完整、新鲜画面。

不支持系统/API/设备、访问拒绝、窗口排除、设备丢失、缺帧、无效缓冲区或几何不明确分别返回稳定错误，如 `WGC_OS_UNSUPPORTED`、`WGC_API_UNAVAILABLE`、`WGC_UNSUPPORTED`、`WGC_DEVICE_UNAVAILABLE`、`WGC_ACCESS_DENIED`、`WGC_WINDOW_EXCLUDED`、`WGC_WINDOW_UNSUPPORTED`、`WGC_DEVICE_LOST`、`WGC_FRAME_UNAVAILABLE`、`WGC_READBACK_FAILED`、`WGC_INVALID_BUFFER`、`WGC_GEOMETRY_UNAVAILABLE`。原生截止时间可返回 `SCREENSHOT_TIMEOUT`；外层 worker 截止时间可能先返回 `UIA_TIMEOUT`。像素和 PNG 超预算分别为 `SCREENSHOT_PIXEL_LIMIT`、`SCREENSHOT_BYTE_LIMIT`。

截图可能包含应用绘出的敏感文字；UIA 密码保护不会修改图像。只读截图仍可能与其他进程的写入交错，几何校验不能检测全部内容变化。根据图像执行点击时使用带新鲜度与身份校验的[视觉流程](DEVELOPER_VISUAL.md)，并验证操作结果。

## 验证

`tests/windows/developer-screenshot-contract.ps1` 使用生成像素验证 PNG、哈希、尺寸和字节预算。`developer-wgc-contract.ps1` 编译实际 COM/D3D 适配器，探测能力，并通过 WARP 合成纹理检查真实 create/copy/flush/map/unmap、行步长、颜色通道和预算，不捕获窗口。能力不可用不算成功捕获。

专用 Windows 交互桌面可显式运行自建窗体验收：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\developer-wgc-acceptance.ps1 -EnableCapture
```

未加 `-EnableCapture` 时，在创建窗口前跳过。脚本不接受外部 HWND，仅在内存中检查自有合成窗体的 PNG 与已知内部像素。它和 [Parallels 开发验收](DEVELOPER_ACCEPTANCE.md) 不能替代任意桌面软件的兼容性验证。

原生接口参见 Microsoft 的 [PrintWindow](https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-printwindow)、[GetWindowRect](https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-getwindowrect)、[CreateForWindow](https://learn.microsoft.com/windows/win32/api/windows.graphics.capture.interop/nf-windows-graphics-capture-interop-igraphicscaptureiteminterop-createforwindow)、[CreateFreeThreaded](https://learn.microsoft.com/uwp/api/windows.graphics.capture.direct3d11captureframepool.createfreethreaded) 和[帧尺寸说明](https://learn.microsoft.com/windows/uwp/audio-video-camera/screen-capture)。
