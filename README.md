# Starbridge · 星桥

连接 AI 与虚拟运行环境，让 AI 助手通过结构化接口管理环境、读取桌面信息。

Starbridge 面向虚拟机与容器，**第一期仅实现 Parallels Desktop 后端**，提供虚拟机管理与 Windows 进程、窗口、控件查询。其他虚拟化平台、容器后端及通用环境接口仍在规划中。

当前为 **0.1 原型**：宿主 CLI 与本地 stdio MCP 服务由 Go 实现，共用一个二进制；Windows 内保留 PowerShell helper。项目与 Parallels、Microsoft、OpenAI 无隶属关系。

## 当前能力

- **管理 Parallels VM**：列表、详情、启动、关机、强制停止、挂起、恢复、克隆和快照管理。
- **按用户策略回收**：关机保留、恢复指定快照或删除 VM；支持默认策略、按 VM 配置和命令行覆盖。
- **读取 Windows 桌面**：进程名称与 PID，窗口标题与位置，以及 UI Automation 暴露的按钮等控件名称、标识、位置和状态。
- **供 AI 调用**：JSON 输出、命令预演、超时控制与本地 MCP 工具。

Windows 桌面能力目前只读；尚未实现点击、输入、截图、任意 guest 命令执行或通用容器管理。窗口和控件读取仍需在真实 Windows VM 完成联调，不能将宿主测试通过视为所有桌面能力已验证。详情见 [验证记录与验收](docs/ACCEPTANCE.md)。

## 快速开始

构建需要 Go 工具链，最低版本见 [go.mod](go.mod)。运行 Parallels 后端需要 macOS，以及支持 CLI 的 Parallels Desktop 版本与授权。从源码项目根目录构建并运行：

```sh
go build -o bin/starbridge ./cmd/starbridge
./bin/starbridge doctor
./bin/starbridge vm list
./bin/starbridge --dry-run vm start "Windows 11"
```

构建后的宿主二进制不依赖 Python，也无需额外安装 MCP SDK。`doctor` 检查宿主 CLI，不会启动 VM，也不会把尚未验证的 Windows 桌面报告为已就绪。

查询 Windows 信息还需安装 Parallels Tools，并从本仓库获取、安装 [Windows helper](guest/windows/README.md)。helper 不会自动安装到虚拟机。

启动本地 MCP 服务：

```sh
./bin/starbridge serve
```

完整说明见 [安装与使用](docs/USAGE.md)，包含 Windows helper 安装、回收策略和 MCP 客户端配置。

## 文档

- [安装与使用](docs/USAGE.md)：环境要求、CLI、回收策略和 MCP。
- [架构与路线](docs/ARCHITECTURE.md)：当前实现、能力边界及多后端规划。
- [验证记录与验收](docs/ACCEPTANCE.md)：当前验证与历史记录、Windows 联调步骤。
- [Windows helper 协议](guest/windows/README.md)：请求、响应、坐标与会话说明。
- [参与贡献](CONTRIBUTING.md)：本地检查与贡献范围。

## 许可

本项目以 **GPLv3（GPL-3.0-only）** 分发，见 [LICENSE](LICENSE)。 第三方依赖及 Go 运行时的许可声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
