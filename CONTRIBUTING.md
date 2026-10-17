# 参与贡献

欢迎改进 Starbridge 的 Parallels 后端、Windows 桌面查询、文档和测试。其他虚拟机与容器支持属于后续规划；新增后端应同时说明实际支持的能力和独立验收方法，避免将规划描述为已实现功能。

## 本地开发

安装满足 [go.mod](go.mod) 要求的 Go 工具链，从项目根目录执行：

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o bin/starbridge ./cmd/starbridge
```

宿主 CLI 与 MCP 服务共用同一个 Go 二进制，不需要 Python、虚拟环境或运行时安装 SDK。MCP 依赖由 Go 模块管理。

可以直接从源码运行，命令预演不要求运行 Parallels：

```sh
go run ./cmd/starbridge --dry-run vm start "Windows 11"
```

主要目录：

- `cmd/starbridge`：可执行程序入口。
- `internal/bridge`：Parallels 调用、guest 协议与回收策略。
- `internal/cli`：命令解析与 JSON 输出。
- `internal/mcpserver`：基于官方 Go SDK 的 MCP stdio 服务。
- `internal/version`：版本信息。
- `guest/windows`：Windows PowerShell helper。

Windows helper 的原生调用需在 Windows 验证。在 Windows PowerShell 5.1 中运行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\guest-smoke.ps1
```

该检查不替代真实应用、交互会话与多显示器 DPI 的验收，详见 [ACCEPTANCE.md](docs/ACCEPTANCE.md)。

## 修改与验证

- 提交 Go 修改前运行 `go fmt ./...`，再执行与变更相关的测试和静态检查。
- 保持 JSON 输出、退出码与错误契约清楚、可预测；标准输出不能夹入诊断日志。
- 宿主命令使用参数数组；guest 查询只接受明确动作和经过验证的参数。
- 副作用操作保留预演、目标确认与超时处理；测试使用专用 VM，删除测试只使用可丢弃的克隆。
- 修改协议或行为时同步更新相关示例与文档，并提供验证该行为的必要测试。文档修订无需新增复述实现的测试。
- 在贡献说明中写明修改目的、已执行检查及尚未验证的环境。替身测试通过不能替代真实 Parallels 或 Windows UIA 验收。
- 提供错误样例或界面输出时，去除真实 VM 标识、用户路径、窗口标题及其他与问题无关的个人信息。

提交贡献前请确认有权按本仓库的 [GPLv3 许可](LICENSE)提供相应内容。
