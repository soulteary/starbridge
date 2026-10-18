# Starbridge · 星桥

让 AI 助手通过 CLI 或 MCP 管理 Parallels Desktop 虚拟机，并在 Windows 中查询、操作和截图测试应用。

Starbridge 在 macOS 上运行 Go 单二进制，通过 Parallels CLI 调用 Windows PowerShell helper。既可以单独执行命令，也可以把开发任务交给支持本地 stdio MCP 的客户端。

## 能做什么

- **虚拟机管理**：列表、详情、启动、正常关机、强制停止、挂起、恢复、克隆及快照管理；按用户策略选择关机保留、恢复快照或删除 VM。
- **应用与控件查询**：进程名称、PID 和启动时间，窗口标题、位置，以及 UI Automation 暴露的控件名称、标识、边界、状态和支持的操作。
- **窗口与输入操作**：UIA 语义操作、窗口聚焦和调整大小、鼠标与按键、中文和 emoji 等 Unicode 文本输入。
- **截图与视觉操作**：按窗口使用 PrintWindow 或 Windows Graphics Capture 截图；观察界面、接收客户端选出的图片坐标、点击一次并返回新观察。MCP 可直接返回 PNG 图片。
- **开发调试**：运行配置好的应用任务、传输和同步文件、安装 helper、等待与断言、编排工作流、导出带哈希清单的证据。
- **可控的 AI 调用**：JSON 输出、离线预演、执行预算、VM 与动作授权，以及相互独立的 VM 写权限和 guest 写权限。

`guest` 提供基础只读查询；`dev` 提供开发动作，其中写操作需要单独授权。UIA 信息取决于应用支持；图片候选的识别由 MCP 客户端的视觉模型或 OCR 完成，Starbridge 不内置识别模型。点击或输入成功派发后，仍需检查应用的实际结果。

## 通过 Homebrew 安装

macOS 13 及以上支持 Apple Silicon 和 Intel，无需安装 Go：

```sh
brew update
brew install soulteary/tap/starbridge
starbridge --version
```

[安装与使用](docs/USAGE.md#homebrew)包含更新、卸载、配套 helper 路径，以及找不到配方时的处理方法。

## 从源码开始

需要 macOS、支持 CLI 的 Parallels Desktop，以及满足 [go.mod](go.mod) 的 Go 工具链：

```sh
go build -o bin/starbridge ./cmd/starbridge
./bin/starbridge --version
./bin/starbridge doctor
./bin/starbridge vm list
./bin/starbridge --dry-run vm start "Windows 11"
```

Windows 功能还需要 Parallels Tools 和同版本 helper。按[安装与使用](docs/USAGE.md)安装完整脚本目录，再检查 guest 能力：

```sh
./bin/starbridge dev system.info VM-UUID
./bin/starbridge dev screenshot.capabilities VM-UUID
```

`VM-UUID` 是占位值，请使用 `vm list` 得到的目标标识。窗口查询、截图和输入需要可访问的已登录交互桌面。也可从 [Releases](https://github.com/soulteary/starbridge/releases) 下载配套安装包。

## 按任务阅读

- [安装与使用](docs/USAGE.md)：Homebrew、源码构建、安装完整 helper、VM 管理、回收策略和 MCP 接入。
- [开发动作入口](docs/DEVELOPER.md)：能力探测、请求格式，以及任务、文件、等待、工作流和证据接口索引。
- [查找控件](docs/DEVELOPER_UI_QUERY.md) · [窗口操作与文本输入](docs/DEVELOPER_UI_ACTIONS.md) · [窗口截图](docs/DEVELOPER_SCREENSHOTS.md) · [视觉观察与点击](docs/DEVELOPER_VISUAL.md)。
- [MCP 开发工具](docs/MCP_DEVELOPER.md) · [VM 与动作授权](docs/MCP_ACCESS.md) · [并发与取消](docs/MCP_CONCURRENCY.md)。
- [架构与能力边界](docs/ARCHITECTURE.md) · [Windows 基础 helper 协议](guest/windows/README.md)。
- [验证与验收](docs/ACCEPTANCE.md) · [桌面查询验收](docs/DESKTOP_TESTING.md) · [开发工作流验收](docs/DEVELOPER_ACCEPTANCE.md)。
- [0.1.0 版本说明](docs/releases/v0.1.0.md) · [参与贡献](CONTRIBUTING.md) · [构建与发布](docs/RELEASING.md)。

## 许可

本项目以 **GPLv3（GPL-3.0-only）** 分发，见 [LICENSE](LICENSE)。第三方依赖及 Go 运行时的许可声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
