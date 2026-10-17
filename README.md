# Starbridge · 星桥

让 AI 助手通过 CLI 或 MCP 管理 Parallels Desktop 虚拟机，查询、操作和截图 Windows 测试应用。

当前为 **0.1 原型**：macOS 宿主使用 Go 单二进制，Windows 内运行 PowerShell helper。

## 当前能力

- **管理 Parallels VM**：列表、详情、启动、关机、强制停止、挂起、恢复、克隆和快照管理。
- **按用户策略回收**：关机保留、恢复指定快照或删除 VM；支持默认策略、按 VM 配置和命令行覆盖。
- **读取 Windows 桌面**：进程名称与 PID，窗口标题与位置，以及 UI Automation 暴露的按钮等控件名称、标识、位置和状态。
- **操作测试应用**：搜索控件、执行 UIA 操作、调整窗口、点击、按键和输入 Unicode 文本；按窗口截图并通过 MCP 返回图片。
- **开发调试**：受配置约束的应用任务、文件传输、helper 安装、等待与断言、工作流和证据导出。
- **供 AI 调用**：JSON 输出、命令预演、超时控制，以及支持 VM 与动作授权的本地 MCP 服务。

`guest` 入口提供只读查询；`dev` 入口提供需要独立写授权的开发动作。控件信息与语义操作取决于应用的 UI Automation 支持，输入和截图也受 Windows 会话、权限与应用实现约束。基础验收见[验收指南](docs/ACCEPTANCE.md)，开发动作的隔离环境验收见[开发验收](docs/DEVELOPER_ACCEPTANCE.md)。

## 快速开始

运行需要 macOS 和支持 CLI 的 Parallels Desktop。可从 [Releases](https://github.com/soulteary/starbridge/releases) 获取已发布的 arm64/amd64 安装包，或使用满足 [go.mod](go.mod) 要求的 Go 工具链从源码构建：

```sh
go build -o bin/starbridge ./cmd/starbridge
./bin/starbridge doctor
./bin/starbridge vm list
./bin/starbridge --dry-run vm start "Windows 11"
```

Windows 查询还需 Parallels Tools 和手动安装的 helper；窗口与控件查询需已登录的桌面。安装、回收策略与 MCP 客户端配置见[安装与使用](docs/USAGE.md)。

开发动作使用 `dev` 入口；环境探测、模块安装与协议见[开发工具](docs/DEVELOPER.md)，点击与文本输入见[窗口操作](docs/DEVELOPER_UI_ACTIONS.md)，窗口截图见[截图接口](docs/DEVELOPER_SCREENSHOTS.md)。

## 文档

- [安装与使用](docs/USAGE.md)：环境要求、CLI、回收策略和 MCP。
- [架构](docs/ARCHITECTURE.md)：组件、执行方式与能力边界。
- [验收指南](docs/ACCEPTANCE.md)：自动检查、真实桌面与 VM 验收。
- [Windows helper 协议](guest/windows/README.md)：请求、响应、坐标与会话说明。
- [参与贡献](CONTRIBUTING.md)：本地检查与贡献范围。
- [发布说明](docs/RELEASING.md)：构建配套安装包与发布流程。

## 许可

本项目以 **GPLv3（GPL-3.0-only）** 分发，见 [LICENSE](LICENSE)。 第三方依赖及 Go 运行时的许可声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
