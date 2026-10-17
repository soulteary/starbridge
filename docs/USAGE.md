# 安装与使用

[返回项目首页](../README.md) · [架构](ARCHITECTURE.md) · [验收指南](ACCEPTANCE.md)

## 环境要求与构建

1. 从源码构建需要满足 [go.mod](../go.mod) 要求的 Go 工具链；安装包中的二进制可直接运行。
2. 宿主运行于 macOS，需要支持 CLI 的 Parallels Desktop 版本与授权。
3. 查询 Windows 时，需要安装 Parallels Tools 的 Windows VM，以及 Windows PowerShell 5.1。
4. 查询窗口和控件时，Windows 中需要已有登录用户；`--current-user` 是否能访问目标桌面，需按[验收步骤](ACCEPTANCE.md)现场验证。

从源码项目根目录构建：

```sh
go build -o bin/starbridge ./cmd/starbridge
./bin/starbridge --version
./bin/starbridge doctor
./bin/starbridge vm list
./bin/starbridge --dry-run vm start "Windows 11"
```

以下示例在源码根目录使用 `./bin/starbridge`；开发时可替换为 `go run ./cmd/starbridge`。使用安装包时，在解压目录以 `./starbridge` 替换下文的 `./bin/starbridge`。

`doctor` 检查宿主 CLI；`doctor --vm VM-UUID` 还会检查 VM、guest 会话、窗口与一个 UIA 根节点，不会启动 VM。诊断结果以 `data.ready` 和 `data.checks` 为准，详见[桌面诊断](DESKTOP_TESTING.md#经-parallels-对照同一窗口)。

CLI 路径按以下优先级选择：`--prlctl` > `STARBRIDGE_PRLCTL` > 兼容变量 `PRLBRIDGE_PRLCTL` > Parallels App 内的 CLI > `PATH`。旧环境变量继续兼容；新配置建议使用 `STARBRIDGE_PRLCTL`。

## 安装 Windows helper

将源码或安装包中的 `guest/windows/desktop-bridge.ps1` 复制到 Windows。在已复制的目录中用 Windows PowerShell 执行：

```powershell
New-Item -ItemType Directory -Force C:\ProgramData\Starbridge | Out-Null
Copy-Item .\guest\windows\desktop-bridge.ps1 C:\ProgramData\Starbridge\desktop-bridge.ps1
```

也可安装到自定义目录，在宿主通过 `--guest-script 'C:\Users\you\Starbridge\desktop-bridge.ps1'` 指定。脚本使用进程级 `-ExecutionPolicy Bypass`，不修改系统执行策略。

协议、会话诊断与参数说明见 [Windows helper](../guest/windows/README.md)。

## 管理虚拟机

将 `VM-UUID`、`SNAPSHOT-UUID` 和 HWND 替换为本机查询所得值。优先使用 UUID，避免改名或重名导致选错 VM。全局选项放在 `vm` / `guest` / `serve` 前。

```sh
./bin/starbridge vm list
./bin/starbridge vm info VM-UUID
./bin/starbridge vm start VM-UUID
./bin/starbridge vm stop VM-UUID
./bin/starbridge vm suspend VM-UUID
./bin/starbridge vm resume VM-UUID
./bin/starbridge --timeout 300 vm clone VM-UUID --name ai-sandbox
./bin/starbridge vm snapshot-create VM-UUID --name clean
./bin/starbridge vm snapshot-list VM-UUID
```

`clone` 以已有 VM 为基础；本版没有从 ISO 自动安装 Windows。`command_completed: true` 仅代表 Parallels 命令成功退出，不代表 Windows 登录、目标程序或桌面已经就绪。随后通过 `vm info` 和 guest 查询验证。

需要强制断电时使用 `vm stop VM-UUID --force --confirm VM-UUID`；正常关机不需要该确认参数。命令超时后先检查实际状态，副作用操作不会自动重试。

## 查询进程、窗口与控件

```sh
./bin/starbridge guest processes VM-UUID --name notepad --include-path
./bin/starbridge guest windows VM-UUID
./bin/starbridge guest windows VM-UUID --pid 1234
./bin/starbridge guest ui VM-UUID --hwnd 0x123456 --max-depth 6 --max-nodes 300
```

进程和窗口列表默认上限为 500；控件树默认深度 6、节点上限 300、worker 超时 10000 毫秒。完整参数范围与字段见 [helper 协议](../guest/windows/README.md)。

HWND 接受正的十进制字符串或带 `0x` / `0X` 前缀的十六进制字符串。

先查询进程和窗口，再使用新取得的 HWND 读取控件树。HWND、PID 和 UIA RuntimeId 都可能失效或被复用，不能当作永久标识。

CLI 成功返回 `ok:true` 与 `data`；失败返回 `ok:false` 与 `error`，退出码为 `1`。guest 的 `meta` 包含协议、会话、采样时间及坐标系。`--help` 与 `--version` 使用普通文本。

宿主 `--timeout` 范围为 1–600 秒，默认 30 秒。stdout 最多保留 8 MiB，超过后终止宿主等待并返回 `OUTPUT_LIMIT_EXCEEDED`。超时、取消或输出超限可能发生在 VM 操作已经开始之后；副作用操作的错误详情会标明 `outcome:unknown`，应先查询实际状态，避免直接重试。

控件矩形属于 Windows guest 屏幕坐标，不可直接用于 macOS 点击。控件树取决于应用可访问性支持；空列表、`is_offscreen:false` 或有效矩形都不足以证明目标可操作。完整限制见[架构与能力边界](ARCHITECTURE.md#桌面能力边界)。

## 用户配置回收行为

默认正常关机，保留 VM 文件：

```sh
./bin/starbridge vm recycle VM-UUID
```

复制[回收策略示例](../examples/recycle-policy.json)，将文件中所有 VM 和快照 UUID 占位值替换为实际值。优先级为：命令行覆盖 > VM 专属策略 > 文件默认策略 > 内置 `stop`。

`--policy` 需要非空文件路径；空字符串或全空白路径会返回 `INVALID_ARGUMENT`，预演模式也遵循此规则。

```sh
./bin/starbridge --dry-run vm recycle VM-UUID --policy examples/recycle-policy.json
./bin/starbridge vm recycle VM-UUID --policy examples/recycle-policy.json --confirm VM-UUID
./bin/starbridge vm recycle VM-UUID --mode restore --snapshot SNAPSHOT-UUID --confirm VM-UUID
./bin/starbridge vm recycle VM-UUID --mode delete --confirm VM-UUID
```

- `stop`：正常关机，保留 VM 和磁盘。
- `restore`：恢复指定快照，丢弃当前 guest 状态；恢复命令带 `--skip-resume`，随后单独决定是否启动。
- `delete`：永久删除 VM 文件与磁盘。

实际执行恢复、删除或强制断电时，需要用 `--confirm` 重复相同 VM 标识；预演无需确认。策略执行失败即返回错误，不自动升级为强制停止或删除。快照不会回滚宿主共享文件、网络或外部服务的变化。

提供 `--policy` 时，目标和文件中所有 `vms` 键必须是 VM UUID，此要求也适用于 `--mode` 覆盖和预演。UUID 大小写与成对大括号不影响匹配，重复 JSON 字段或重复 UUID 会使文件被拒绝；`--confirm` 仍须与原始输入完全相同。未提供策略文件时可使用 VM 名称，预演始终不查询 Parallels。

也可以直接运行快照恢复与删除命令：

```sh
./bin/starbridge vm snapshot-restore VM-UUID --snapshot SNAPSHOT-UUID --confirm VM-UUID
./bin/starbridge vm delete VM-UUID --confirm VM-UUID
```

## MCP 接入

Codex 等本地客户端可以调用 CLI，也可以配置 stdio MCP。构建二进制后，将命令路径替换为该二进制的绝对路径。使用 Codex CLI 时：

```sh
codex mcp add starbridge -- /absolute/project/bin/starbridge serve
```

默认提供 `vm_list`、`vm_info`、`guest_processes`、`guest_windows`、`guest_ui_tree`。如需向客户端提供启动、关机、挂起和恢复工具，将启动参数改为：

```sh
/absolute/project/bin/starbridge serve --allow-vm-write
```

配置示例见 [Codex MCP](../examples/codex-mcp.toml)。MCP 结果沿用上述 JSON 信封；工具错误同时标记 `isError`。本版不通过 MCP 暴露删除、快照恢复或任意 shell。

同一 MCP 服务最多并行执行 4 个后端调用，生命周期写操作按目标协调；排队请求支持取消。名称与 UUID 别名、CLI 进程边界详见 [MCP 并发与取消](MCP_CONCURRENCY.md)。

ChatGPT 接入需要所用环境支持本地 stdio 工具；普通网页聊天不能直接访问 Mac 上的本地进程。当前服务仅提供本地 stdio 接口。

需要限制 MCP 可访问的 VM 与动作时，可使用 `serve --access-policy FILE`。策略按 UUID 授权，并对名称先解析再执行；详见 [MCP 的 VM 与动作授权](MCP_ACCESS.md)。

## 常见检查

- **找不到 CLI**：检查 Parallels 安装与授权，必要时显式设置 `--prlctl` 或 `STARBRIDGE_PRLCTL`。
- **无法连接 Parallels Service**：先检查本机服务状态和执行环境的访问权限，不能仅凭该错误断定 Parallels 未安装。
- **进程查询成功但没有窗口**：检查登录用户、会话、锁屏状态及应用权限，继续按[桌面验收](ACCEPTANCE.md#桌面端到端验收)排查。
- **命令超时**：guest 或 VM 工作可能仍在继续，先查询状态，再决定是否重试。
