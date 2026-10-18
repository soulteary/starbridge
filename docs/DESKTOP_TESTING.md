# 桌面验收

准备专用 Windows 测试 VM，安装 Parallels Tools 与[匹配版本的完整 helper 目录](../guest/windows/README.md)，并登录解锁的可见桌面。只对自己创建的夹具窗口验收；VM 生命周期操作见[验收指南](ACCEPTANCE.md#虚拟机操作验收)。普通 CI 的合成 contracts 不会替你完成本页的真实环境检查。

## Windows 内验证已知窗口

在 Windows 项目目录使用 Windows PowerShell 5.1：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\desktop-acceptance.ps1
```

脚本启动 WinForms 夹具，检查其 PID、HWND、标题、非空窗口矩形、中文按钮及启用状态，并验证根节点查询恰好返回深度为 0 的一个节点。脚本结束时关闭夹具并清理临时文件；空列表或 Session 0 错误会使验收失败。

此步骤验证 Windows helper 与真实 UIA。普通 CI 的执行范围见[自动化检查](ACCEPTANCE.md#自动化检查范围)。

## 经 Parallels 对照同一窗口

先从宿主执行只读诊断：

```sh
./bin/starbridge doctor --vm TEST-VM-UUID
```

诊断在同一宿主超时预算内依次检查 CLI、VM 信息、guest 协议与会话、一个窗口及其 UIA 根。检查 `data.checks` 和 `data.ready`：`ok:true` 表示诊断已完成，`data.ready:true` 表示本轮读到一个窗口根。该结果的范围仅限本次观察，不保证所有应用或当前输入桌面均可访问。

在 Windows 中单独运行夹具，保持窗口开启：

```powershell
powershell.exe -NoProfile -Sta -ExecutionPolicy Bypass -File .\tests\windows\desktop-fixture.ps1 -MetadataPath "$env:TEMP\starbridge-fixture.json"
```

从元数据文件取得本次夹具的 PID 和 HWND，在宿主替换下列占位值：

```sh
./bin/starbridge guest processes TEST-VM-UUID --pid PID
./bin/starbridge guest windows TEST-VM-UUID --pid PID
./bin/starbridge guest ui TEST-VM-UUID --hwnd HWND
```

确认进程和窗口对应夹具，核对标题、中文按钮、用户、会话及矩形。宿主使用的已安装 helper 应与 Windows 内验收的版本一致。完成后关闭夹具窗口。

## 开发动作与图像验收

先按[开发验收](DEVELOPER_ACCEPTANCE.md)配置专用 `starbridge-fixture` profile。该流程自动覆盖任务身份、一次 UIA Invoke、计数器后置条件、默认 PrintWindow 截图及退出；以下新增能力需要单独验收。

- **Unicode 输入**：对夹具的 `Synthetic editor` 文本框显式设置窗口和控件焦点，再按[文字输入契约](DEVELOPER_UI_ACTIONS.md)发送合成中文与 emoji。核对事件计数，并用 UIA 或人工查看控件实际内容。CR/LF/Tab 是原样字符，不会转为 Enter/Tab 按键；现有夹具是单行文本框，不能据此宣称多行输入通过。需要多行场景时使用自己创建的多行控件，分别记录应用如何处理这些字符。拒绝、部分输入或未知结果后先检查状态，不自动补发文本。
- **观察与视觉点击**：按[视觉流程](DEVELOPER_VISUAL.md)对同一夹具执行 `ui.observe`，确认截图可辨认、UIA 状态和图片物理原点，再选择已知计数按钮的一处明确候选。一次 `ui.click` 后检查新观察与计数器增量。移动或缩放窗口、过期或修改令牌时，确认旧观察被拒绝且没有点击。合成坐标测试不能替代该检查；动作返回成功也不代表候选选对。
- **截图后端**：分别记录 `print_window` 与显式 `windows_graphics_capture`。后端不自动切换；先调用 `screenshot.capabilities` 可了解 OS/API/硬件支持，但不能证明当前窗口或输入桌面可捕获。检查图片与夹具内容、尺寸和位置是否一致；PNG 可解码、哈希正确或 API 返回成功都不能证明视觉正确。GPU 应用、HDR、窗口遮挡和自绘边框按各自环境记录，捕获范围保持为指定 HWND。

WGC 另提供只捕获自身 WinForms 夹具的选择性脚本：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\developer-wgc-acceptance.ps1 -EnableCapture
```

不传 `-EnableCapture` 时，脚本在创建窗口前跳过。启用后会显示自己的合成窗体，以 WGC 捕获到内存并检查已知颜色像素；它不接受外部 HWND，不保存或上传图片。该脚本验证 Windows 本地捕获，不能单独证明 Parallels 传输或任意应用的兼容性。帧尺寸、裁剪与边界定义见[截图说明](DEVELOPER_SCREENSHOTS.md)。

## 环境与异常场景

- **显示器与 DPI**：移动夹具至不同缩放比例的显示器，检查物理坐标、负坐标、矩形与图片原点。WGC 的可见边框与 Win32 外框可能不同；视觉点击应使用本次观察中的映射，不能假定两者相同。guest 坐标不能直接作为 macOS 屏幕坐标使用。
- **窗口生命周期**：使用深度 0、节点数 1 的限制复验；关闭窗口后重查旧 HWND，确认调用者能处理窗口消失或标识被复用。
- **会话与权限**：分别检查非交互会话、锁屏、UAC 安全桌面及高权限应用，记录实际错误或可见范围。进程查询成功不能证明目标桌面可访问。
- **阻塞 provider**：需要验收 UIA 挂起处理时，使用受控测试 provider，核对超时响应和 worker 退出情况；普通 WinForms 夹具不模拟此场景。
- **应用差异**：记录 UIA 不可用、拒绝前台激活、完整性级别/UIPI 限制、WGC 不支持或画面为空等实际结果。受控示例成功不代表所有窗口均可操作；失败后不会自动扩大截图范围或重试写操作。

按[验收记录要求](ACCEPTANCE.md#保存验收结果)保存环境、输出与实际观察，便于在 Windows on Arm、x64 和不同登录方式下对照。
