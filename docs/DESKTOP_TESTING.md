# 可重复的桌面正向验收

使用专用 Windows 测试 VM，安装 Parallels Tools、匹配版本的 helper，并登录可见桌面。以下测试不会启动、停止或回收 VM。

## Windows 内直接验收

在 Windows PowerShell 5.1 中执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\desktop-acceptance.ps1
```

测试启动并关闭自己的 WinForms 窗口，要求实际找到其 PID、HWND、标题、非空矩形、中文按钮以及根节点。空列表和 Session 0 错误不能算通过。脚本不点击按钮、不操作已有应用。普通无桌面的 CI 继续运行 guest smoke，正向验收需显式执行。

## 经 Parallels 对照

从宿主执行：

```sh
./bin/starbridge doctor --vm TEST-VM-UUID
```

此命令共享一个宿主超时预算，依次检查 CLI、VM 信息、guest 协议与会话、一个窗口及其 UIA 根。它不会启动 VM。`ok:true` 表示诊断完成；只有 `data.ready:true` 表示本轮观察到一个窗口根，具体结果见 `data.checks`。空窗口不会被报告为桌面就绪，单个根成功也不保证所有应用或当前输入桌面可访问。

需要对照已知窗口时，在 Windows 中单独运行夹具，保持窗口开启：

```powershell
powershell.exe -NoProfile -Sta -ExecutionPolicy Bypass -File .\tests\windows\desktop-fixture.ps1 -MetadataPath C:\Temp\starbridge-fixture.json
```

读取元数据文件中的 PID/HWND，分别从宿主运行 `guest processes --pid PID`、`guest windows --pid PID`、`guest ui --hwnd HWND`，核对中文按钮、会话、用户与坐标。结束后关闭夹具窗口。

## 单独记录环境差异

记录 Windows/Parallels/Tools 版本、arm64 或 x64、缩放比例、显示器排列与登录方式。将夹具移到另一缩放比例的显示器后复验矩形；锁屏、UAC 与挂死 provider 单独验收。不要把普通 CI 通过替代这些结果。

当前提交在 macOS 使用替身验证诊断流程；Windows 正向夹具、真实 UIA 与 Parallels 传输尚未在本次修改中实际运行。
