# 桌面验收

准备专用 Windows 测试 VM，安装 Parallels Tools 与[匹配版本的 helper](../guest/windows/README.md)，并登录可见桌面。以下桌面测试只启动和关闭自己的夹具窗口，VM 生命周期操作见[验收指南](ACCEPTANCE.md#虚拟机操作验收)。

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

## 环境与异常场景

- **显示器与 DPI**：移动夹具至不同缩放比例的显示器，检查物理坐标、负坐标和矩形边界；guest 坐标不能直接作为 macOS 屏幕坐标使用。
- **窗口生命周期**：使用深度 0、节点数 1 的限制复验；关闭窗口后重查旧 HWND，确认调用者能处理窗口消失或标识被复用。
- **会话与权限**：分别检查非交互会话、锁屏、UAC 安全桌面及高权限应用，记录实际错误或可见范围。进程查询成功不能证明目标桌面可访问。
- **阻塞 provider**：需要验收 UIA 挂起处理时，使用受控测试 provider，核对超时响应和 worker 退出情况；普通 WinForms 夹具不模拟此场景。

按[验收记录要求](ACCEPTANCE.md#保存验收结果)保存环境、输出与实际观察，便于在 Windows on Arm、x64 和不同登录方式下对照。
