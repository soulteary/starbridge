# 验证与验收

自动化检查验证代码和协议契约；桌面访问与虚拟机操作还需在目标环境验收。具体提交的检查结果见 [Tests 运行记录](https://github.com/soulteary/starbridge/actions/workflows/test.yml)，发布门槛见 [Release 工作流](https://github.com/soulteary/starbridge/blob/main/.github/workflows/release.yml)。

## 自动化检查范围

[测试工作流](https://github.com/soulteary/starbridge/blob/main/.github/workflows/test.yml) 包含：

- Linux 与 macOS：Go 测试、竞态检查、静态检查、二进制构建，以及不编译或发布安装包的打包契约测试。宿主测试通过替身验证 Parallels 命令、guest 响应与 MCP 协议，不操作真实 VM。
- Windows PowerShell 5.1：helper smoke 与共享参数契约测试，覆盖进程查询、JSON、数组、输入类型与边界；同时解析桌面验收脚本。smoke 可接受空窗口列表或明确的非交互会话错误，UIA 正向查询另按下节验收。

在源码目录复现宿主检查：

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o bin/starbridge ./cmd/starbridge
./tests/release/package-release-test.sh
```

在 Windows 项目目录运行基础检查：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\guest-smoke.ps1
```

## 桌面端到端验收

按[桌面验收指南](DESKTOP_TESTING.md)先验证已知窗口与中文按钮，再经 Parallels 对照同一窗口的 PID、HWND、会话与坐标。指南还包含多显示器 DPI、窗口生命周期及权限场景；这些结果需要在对应 Windows 环境记录。

## 虚拟机操作验收

使用专用测试 VM 或可丢弃的克隆，先通过 `vm list` 确认 UUID。命令与回收策略格式见[安装与使用](USAGE.md)。

1. 每种操作先用 `--dry-run` 核对目标和参数，再验证启动、正常关机、挂起、恢复与克隆。通过 `vm info` 和 guest 查询检查操作后的实际状态与就绪情况。
2. 在 guest 本地磁盘创建标记文件并保存快照，修改文件后恢复快照，确认文件和 VM 状态符合预期。恢复、删除及强制断电按命令要求提供确认标识。
3. 分别验证 `recycle` 的 `stop`、`restore`、`delete` 模式，以及默认策略、VM 专属策略和命令行覆盖。删除场景只使用可丢弃的克隆；确认策略失败后没有自动改用其他操作。
4. 记录超时或连接中断后的实际 VM 状态，再决定是否重试。命令返回成功后仍需检查 guest 是否已完成启动或关机。

## 保存验收结果

每次验收记录提交或发布版本、Windows 与 Parallels / Tools 版本、CPU 架构、登录方式、测试场景、实际结果和相关 JSON 输出。桌面场景补充显示器排列与缩放比例。按自动化检查、Windows 内验收、Parallels 传输和 VM 操作分别记录结果，便于复验。
