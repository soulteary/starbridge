# 验证记录与桌面验收

记录日期：2026-10-17（Asia/Shanghai）。

## Go 迁移验证

本轮将宿主 CLI 与 MCP 服务改为 Go 单二进制，Windows PowerShell helper 保留。以下为本轮实际执行结果，下方 Python 版记录仅供追溯。

- 验证环境：macOS / arm64，Go 1.27.2；模块要求 Go 1.25.0 或更新版本，官方 MCP Go SDK 锁定为 v1.8.0。
- `go test -race -cover ./...` 通过，共 39 个顶层测试，包含子测试共 155 项通过。覆盖率：bridge 86.4%、CLI 92.9%、MCP 94.7%。入口包主要负责信号与退出，另通过构建后的实际二进制复验。
- `go vet ./...` 与 `go build -trimpath -o bin/starbridge ./cmd/starbridge` 通过，Go 源码已格式化。
- 单元与子进程测试覆盖 VM 参数数组、三种回收策略及优先级、确认条件、Unicode、BOM、guest 元数据与交互会话、超时/取消、stdout 上限、错误信封，以及显式空快照参数不得执行回收的回归情况。测试替身不接触真实 VM。
- MCP 测试覆盖默认 5 个只读工具、显式开启后的 9 个工具、schema 与参数限制、错误 `isError`、文本与结构化结果一致、并发请求和取消传播，包含真实 OS 管道的 stdio 初始化与关闭。
- 构建后从源码目录之外运行二进制，验证版本与回收预演；另用独立的官方 Python MCP 客户端 1.30.0 连接 Go 二进制，复验初始化、5/9 工具发现、Unicode、guest 元数据、非法整数与 guest 错误传播。此客户端仅用于跨语言测试，项目运行无需 Python。
- 使用 Go 二进制只读连接真实 Parallels：`doctor` 返回 `prlctl version 27.0.3 (58680)`，`vm list` 成功读取 4 台 VM；这些操作没有启动、停止、修改或删除 VM。
- Windows helper 与 Windows smoke 脚本内容保持一致，未在 Windows 执行；真实 Windows UIA、VM 副作用操作与 GitHub CI 仍待下方验收。

## Python 版整理与安装验证（历史）

迁移前曾将 Python 包、命令与 MCP 服务统一命名为 Starbridge，并验证打包与安装。以下仅保留当时的验证记录，不描述当前 Go 版的运行方式或交付物。

- 更名后的 45 个 Python 测试全部通过，包含实际 stdio MCP 协议测试；Parallels 与 guest 查询使用测试替身。
- 验证 `STARBRIDGE_PRLCTL`、旧兼容变量 `PRLBRIDGE_PRLCTL` 和显式参数在真实执行路径与预演路径中的优先级。
- 成功构建 `starbridge-0.1.0` wheel 和源码分发包，并检查名称、版本、GPL-3.0-only 元数据及当时的许可记录。
- 在独立虚拟环境安装 wheel 后，从仓库目录外验证 `starbridge --version`、模块命令预演和 MCP 工具发现、错误返回。确认加载的是安装包，而非依赖源码目录才能导入。
- 检查源码分发包包含 Windows helper、使用与架构文档、测试、示例及 GitHub Actions 配置。当时的 wheel 只含 Python 包及许可记录，helper 需从源码获取。
- 该次验证没有实际执行任何 VM 生命周期操作；下方列出的 Windows 桌面和 GitHub CI 验收仍未完成。

## 早期原型验证记录（重命名前）

以下为 Parallels Desktop Bridge 原型的既有验证事实。真实 Parallels 版本与 VM 列表读取结果继续作为历史验证记录保留，不表示 Go 迁移后重新执行过这些宿主检查。Windows 桌面与 GitHub CI 尚未实际执行。

- macOS 上使用 Python 3.13.13。
- 45 个 Python 测试全部通过。其中 43 个验证命令参数、JSON、输入限制、超时及回收策略；2 个使用官方 MCP Python SDK 1.30.0，验证真实 stdio 连接、工具发现、查询、错误及读写工具配置。
- MCP 测试中的 Parallels 和 guest 响应使用独立可执行测试替身，不代表 Windows UIA 已通过。
- 安装项目与 MCP 可选依赖成功。
- 真实 `prlctl --version`：27.0.3 (58680)。
- 通过本项目 CLI 成功读取真实 VM 列表。
- 预演命令正确生成参数；中文过滤条件和含空格的 Windows 脚本路径能够编码往返。

该次验证没有实际执行启动、关机、克隆、快照恢复或删除，也没有复制文件到现有 VM。上述动作只验证命令构造、确认条件和失败处理。名为 Windows 11 的本机 VM 在检查时处于停止状态，未用于桌面验收。

## 尚未验证

- Windows PowerShell 5.1 运行和 C# native helper 编译。
- Windows on Arm / x64 的 UIA 行为。
- `prlctl exec --current-user` 是否进入目标用户桌面。
- 真实应用的进程、窗口、按钮、中文内容和多显示器 DPI。
- 挂死 UIA provider 的进程隔离效果。
- VM 副作用命令的端到端执行效果。
- GitHub Actions 工作流的实际运行结果；工作流和 Windows smoke 测试已提供，未在 GitHub 上提交运行。

## Windows 基础检查

在 Windows 项目目录中使用 Windows PowerShell 5.1：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\guest-smoke.ps1
```

该测试覆盖脚本解析、进程查询与错误契约。窗口存在性和 UIA 能力必须继续在登录用户的可见桌面验收。

## 桌面端到端验收

1. 准备专用 Windows 测试 VM 并登录；安装 Parallels Tools 和 `guest/windows/desktop-bridge.ps1`。使用测试 VM UUID。
2. 手动打开记事本，从宿主运行 `guest processes`，按名称找到它的 PID。
3. 按 PID 运行 `guest windows`，确认标题、HWND、矩形和登录会话。进程列表成功不足以证明桌面访问成功。
4. 使用本次查询的 HWND 运行 `guest ui`，对照实际应用检查控件名称和位置。某个应用未暴露 UIA 控件时，如实记录覆盖缺口。
5. 将窗口移至不同 DPI 的显示器，检查 `coordinate_space`、负坐标及边界。不要把 guest 坐标直接用于 macOS 点击。
6. 使用 `--max-depth 0` 和 `--max-nodes 1` 检查有界输出；关闭应用后再次查询旧 HWND，处理窗口消失或 HWND 被复用。
7. 检查 Session 0、非交互会话、锁屏和高权限应用的失败行为。空结果不等于目标程序没有窗口。
8. 仅使用专用 VM 测试三种回收策略。先预演，再实际关机；验证快照中的测试文件恢复；删除场景仅使用可丢弃的克隆。超时后先查询状态，避免盲目重试。

若宿主报告无法连接 Parallels Service，先确认 Parallels 服务状态及执行环境对本机服务的访问权限。本次在受限环境中曾出现该错误，在获准访问宿主服务的环境中读取成功；不能仅凭该提示断定 Parallels 未安装。
