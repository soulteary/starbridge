# Starbridge · 星桥

让 AI 助手通过 CLI 或 MCP 管理 Parallels Desktop 虚拟机，读取 Windows 进程、窗口与控件信息。

当前为 **0.1 原型**：macOS 宿主使用 Go 单二进制，Windows 内运行 PowerShell helper。

## 当前能力

- **管理 Parallels VM**：列表、详情、启动、关机、强制停止、挂起、恢复、克隆和快照管理。
- **按用户策略回收**：关机保留、恢复指定快照或删除 VM；支持默认策略、按 VM 配置和命令行覆盖。
- **读取 Windows 桌面**：进程名称与 PID，窗口标题与位置，以及 UI Automation 暴露的按钮等控件名称、标识、位置和状态。
- **供 AI 调用**：JSON 输出、命令预演、超时控制，以及支持 VM 与动作授权的本地 MCP 服务。

桌面接口只读，暂不支持点击、输入或截图；控件信息取决于应用的 UI Automation 支持。真实桌面的验证方法见[验收指南](docs/ACCEPTANCE.md)。

## 快速开始

运行需要 macOS 和支持 CLI 的 Parallels Desktop。可从 [Releases](https://github.com/soulteary/starbridge/releases) 获取已发布的 arm64/amd64 安装包，或使用满足 [go.mod](go.mod) 要求的 Go 工具链从源码构建：

```sh
go build -o bin/starbridge ./cmd/starbridge
./bin/starbridge doctor
./bin/starbridge vm list
./bin/starbridge --dry-run vm start "Windows 11"
```

Windows 查询还需 Parallels Tools 和手动安装的 helper；窗口与控件查询需已登录的桌面。安装、回收策略与 MCP 客户端配置见[安装与使用](docs/USAGE.md)。

## 文档

- [安装与使用](docs/USAGE.md)：环境要求、CLI、回收策略和 MCP。
- [架构](docs/ARCHITECTURE.md)：组件、执行方式与能力边界。
- [验收指南](docs/ACCEPTANCE.md)：自动检查、真实桌面与 VM 验收。
- [Windows helper 协议](guest/windows/README.md)：请求、响应、坐标与会话说明。
- [参与贡献](CONTRIBUTING.md)：本地检查与贡献范围。
- [发布说明](docs/RELEASING.md)：构建配套安装包与发布流程。

## 许可

本项目以 **GPLv3（GPL-3.0-only）** 分发，见 [LICENSE](LICENSE)。 第三方依赖及 Go 运行时的许可声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
