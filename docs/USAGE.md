# 安装与使用

[返回项目首页](../README.md) · [架构与能力边界](ARCHITECTURE.md) · [开发动作](DEVELOPER.md) · [验证与验收](ACCEPTANCE.md)

## 1. 构建宿主程序

宿主需要 macOS 和支持 CLI 的 Parallels Desktop。源码构建的最低 Go 版本见 [go.mod](../go.mod)；正式打包使用 `.go-version` 固定的工具链，见[发布指南](RELEASING.md)。

在项目根目录执行：

```sh
go build -o bin/starbridge ./cmd/starbridge
./bin/starbridge --version
./bin/starbridge doctor
./bin/starbridge vm list
```

本文使用源码目录下的 `./bin/starbridge`；也可替换为 `go run ./cmd/starbridge`。[Releases](https://github.com/soulteary/starbridge/releases) 有配套资产时，解压后用 `./starbridge` 代替。二进制与 Windows helper 应来自同一个源码提交或发布包。

`doctor` 检查宿主 CLI，不会启动 VM。CLI 路径依次取 `--prlctl`、`STARBRIDGE_PRLCTL`、兼容变量 `PRLBRIDGE_PRLCTL`、Parallels App 内的 CLI、`PATH`。覆盖路径时使用：

```sh
./bin/starbridge --prlctl '/Applications/Parallels Desktop.app/Contents/MacOS/prlctl' doctor
```

全局选项 `--prlctl`、`--timeout`、`--guest-script`、`--developer-script` 和 `--dry-run` 放在 `doctor` / `vm` / `guest` / `dev` / `serve` 之前。动作选项可放在 VM 参数前后；`--help` 查看相应入口。

## 2. 安装 Windows helper

Windows VM 需要安装 Parallels Tools，并提供 Windows PowerShell 5.1。窗口、UIA、截图和输入还需要可访问的已登录交互桌面；`--current-user` 本身不能保证满足这些条件。

在 Windows 中打开已复制的项目或配套包根目录，把完整 helper 树复制到默认位置：

```powershell
$destination = 'C:\ProgramData\Starbridge'
New-Item -ItemType Directory -Force $destination | Out-Null
Copy-Item .\guest\windows\desktop-bridge.ps1 $destination -Force
Copy-Item .\guest\windows\developer-bridge.ps1 $destination -Force
Copy-Item .\guest\windows\developer $destination -Recurse -Force
```

目标目录需要当前安装用户有写权限。基础 `guest` 查询只需要 `desktop-bridge.ps1`；开发功能需要额外的 `developer-bridge.ps1` 和整个 `developer/`，包括 workers 和 `.cs` 源码。以上复制适合首次安装；`Copy-Item -Force` 不会清除旧版遗留模块，手动升级应使用新目录并切换入口路径，或使用整树安装动作。不要只替换入口脚本。

也可选择自己有权限的目录，分别通过 `--guest-script` 和 `--developer-script` 指向对应 Windows 绝对路径。宿主调用使用进程级 `-ExecutionPolicy Bypass`，不修改系统执行策略。需要可回滚、带哈希校验的整树安装时，使用[文件与 helper 安装](DEVELOPER_FILES.md)中的 `bootstrap.install`。

## 3. 识别 VM 并检查能力

从 `vm list` 选择目标 UUID。下文使用合成标识；实际操作前将它替换为自己查询到的 UUID：

```sh
VM_UUID='11111111-1111-1111-1111-111111111111'
./bin/starbridge vm info "$VM_UUID"
./bin/starbridge doctor --vm "$VM_UUID"
./bin/starbridge dev system.info "$VM_UUID"
./bin/starbridge dev screenshot.capabilities "$VM_UUID"
```

`doctor --vm` 检查 VM、基础 helper、会话、窗口和一个 UIA 根节点，不会启动 VM 或安装脚本。即使命令成功，也要查看 `data.ready` 与 `data.checks`。

`system.info` 返回已安装开发动作和 helper 指纹；动作存在不代表当前会话和应用可用。`screenshot.capabilities` 探测截图后端的 OS、API 与设备支持，并单独给出会话信息，不捕获窗口。具体返回字段见[开发协议](DEVELOPER.md)和[截图接口](DEVELOPER_SCREENSHOTS.md)。

## 4. 管理与回收虚拟机

先预演，再对已经确认的测试 VM 执行操作：

```sh
./bin/starbridge --dry-run vm start "$VM_UUID"
./bin/starbridge vm start "$VM_UUID"
./bin/starbridge vm stop "$VM_UUID"
./bin/starbridge vm suspend "$VM_UUID"
./bin/starbridge vm resume "$VM_UUID"
./bin/starbridge --timeout 300 vm clone "$VM_UUID" --name ai-sandbox
./bin/starbridge vm snapshot-create "$VM_UUID" --name clean
./bin/starbridge vm snapshot-list "$VM_UUID"
```

`clone` 从已有 VM 创建克隆，不提供从 ISO 自动安装 Windows。`command_completed:true` 只代表 Parallels 命令成功退出，不能代替 Windows 登录或应用就绪检查。

正常关机不需要确认参数；强制停止、快照恢复和删除要求 `--confirm` 原样重复目标标识：

```sh
./bin/starbridge --dry-run vm stop "$VM_UUID" --force
./bin/starbridge vm stop "$VM_UUID" --force --confirm "$VM_UUID"
./bin/starbridge vm snapshot-restore "$VM_UUID" --snapshot SNAPSHOT-UUID --confirm "$VM_UUID"
./bin/starbridge vm delete "$VM_UUID" --confirm "$VM_UUID"
```

`recycle` 的默认行为是正常关机并保留 VM 文件：

```sh
./bin/starbridge vm recycle "$VM_UUID"
```

用户可选择三种回收模式：

- `stop`：正常关机，保留 VM 和磁盘。
- `restore`：恢复指定快照，丢弃当前 guest 状态；命令使用 `--skip-resume`，之后单独决定是否启动。
- `delete`：永久删除 VM 文件与磁盘。

复制[回收策略模板](../examples/recycle-policy.json)，先替换其中**所有** VM 和快照占位值，再使用：

```sh
./bin/starbridge --dry-run vm recycle "$VM_UUID" --policy recycle-policy.json
./bin/starbridge vm recycle "$VM_UUID" --policy recycle-policy.json --confirm "$VM_UUID"
./bin/starbridge vm recycle "$VM_UUID" --mode restore --snapshot SNAPSHOT-UUID --confirm "$VM_UUID"
./bin/starbridge vm recycle "$VM_UUID" --mode delete --confirm "$VM_UUID"
```

优先级为命令行覆盖 > VM 专属策略 > 文件默认策略 > 内置 `stop`。恢复、删除和强制停止在预演时不需要确认；实际执行时必须确认。失败不会自动升级为强制停止或删除。快照不回滚宿主共享文件、网络或外部服务的变化。

提供 `--policy` 时，目标和文件中所有 `vms` 键都必须是 UUID，包括预演与 `--mode` 覆盖场景。匹配忽略 UUID 大小写和成对大括号；重复字段、重复 UUID 或空白策略路径会被拒绝。`--confirm` 仍与原始输入逐字匹配。没有策略文件时，VM 名称仍可用于相应命令。

## 5. 查询进程、窗口和控件

```sh
./bin/starbridge guest processes "$VM_UUID" --name notepad
./bin/starbridge guest processes "$VM_UUID" --pid 42 --include-path
./bin/starbridge guest windows "$VM_UUID"
./bin/starbridge guest windows "$VM_UUID" --pid 42
./bin/starbridge guest ui "$VM_UUID" --hwnd 0x123 --max-depth 6 --max-nodes 300
```

PID 和 HWND 都是合成示例。先取得新进程与窗口记录，再查询目标控件。进程路径默认不读取；只有需要时才加 `--include-path`。`--include-hidden` 可用于窗口查询。

列表默认最多 500 项；控件树默认深度 6、最多 300 节点、worker 超时 10000 毫秒。HWND 接受正的十进制字符串或 `0x` / `0X` 十六进制字符串。字段与完整参数范围见[基础 helper 协议](../guest/windows/README.md)。

这些读取不改变应用状态。需要按名称、控件类型或父级搜索，或读取支持的 UIA pattern 时，使用[开发控件查询](DEVELOPER_UI_QUERY.md)。写动作使用当前 HWND、PID 和启动时间共同识别目标，不把控件树内的 `node_id` 当作持久选择器。

## 6. 截图、输入与视觉操作

开发动作统一使用 `dev ACTION VM --request FILE`。文件是一个 UTF-8 JSON **参数对象**，不含 `action` / `params` 外层；省略文件相当于 `{}`。例如将以下合成身份替换成当前窗口身份，保存为 `observe.json`：

```json
{
  "hwnd": "0x123",
  "pid": 42,
  "start_time_utc": "2026-01-02T03:04:05Z",
  "include_uia": true,
  "capture_method": "print_window"
}
```

```sh
./bin/starbridge --dry-run dev ui.observe "$VM_UUID" --request observe.json
./bin/starbridge dev ui.observe "$VM_UUID" --request observe.json
```

`ui.observe` 返回截图和可选 UIA 信息。没有完整控件树时，客户端仍可根据图片提出候选点或矩形，再通过 `ui.click` 点击并重新观察。Starbridge 不内置 OCR 或目标识别模型。观察有时效，不能长期保存后重复点击。完整请求、坐标映射和 Python 示例见[视觉流程](DEVELOPER_VISUAL.md)。

- [窗口截图](DEVELOPER_SCREENSHOTS.md)：`screenshot.capture` 默认 PrintWindow；显式 `capture_method:"windows_graphics_capture"` 选择 WGC，不会自动回退到桌面截图。
- [窗口与 UIA 操作](DEVELOPER_UI_ACTIONS.md)：`ui.act`、`window.focus/resize`、`input.mouse/key/text`，写动作需 `--allow-write`，包括预演。
- [Unicode 文本输入](DEVELOPER_UI_ACTIONS.md#unicode-文本输入)：`input.text` 输入中文和 emoji 等原样字符；它使用当前焦点，不隐式切换控件。CR/LF/Tab 按字符发送，不等同于 Enter/Tab 按键。
- [任务与调试](DEVELOPER.md)：配置好的应用任务、文件传输、等待、工作流和证据导出。

坐标是 Windows guest 屏幕物理像素，可能为负数；不能直接使用 macOS 查看器坐标。UIA 的可用性、窗口可见标记和图片内容均不能替代操作后的业务断言。

## 7. 接入 MCP 客户端

Starbridge 只提供本地 stdio MCP，不监听网络端口。客户端需能够启动本地进程。构建后可使用[Codex 配置模板](../examples/codex-mcp.toml)，或运行：

```sh
codex mcp add starbridge -- /absolute/project/bin/starbridge serve
```

命令路径需要替换为二进制的绝对路径。默认工具为 `vm_list`、`vm_info`、`guest_processes`、`guest_windows`、`guest_ui_tree`。需要 VM 启动、正常关机、挂起、恢复时，加 `serve --allow-vm-write`。

开发工具需单独启用；如需文件类动作，先建立一个供服务使用的宿主目录：

```sh
mkdir -p automation-workspace
./bin/starbridge serve --developer-tools --allow-guest-write \
  --developer-workspace "$PWD/automation-workspace"
```

`--developer-tools` 开放开发读取工具；`--allow-guest-write` 另行开放开发写工具，不授权 VM 电源操作。省略 workspace 时不开放需要宿主文件的动作。MCP 工具名使用 `dev.` 前缀，如 `dev.ui.observe`；参数为 `{"vm":"VM-UUID","params":{...}}`，与 CLI 参数文件的外层不同。截图和视觉结果可直接作为 MCP PNG 图片交给客户端。

需要限制 VM 与动作时，加 `--access-policy FILE`。授权既检查外层工具，也检查工作流、视觉流程等受限嵌套调用。详见[MCP 开发工具](MCP_DEVELOPER.md)、[访问策略](MCP_ACCESS.md)与[并发和取消](MCP_CONCURRENCY.md)。MCP 不暴露 VM 删除、快照恢复或请求内任意 shell；是否能接入 ChatGPT 等客户端取决于其运行环境能否使用这一本地 stdio 服务。

## 8. 结果、预演和超时

普通命令成功输出一行 `ok:true` JSON，失败输出 `ok:false` 与 `error` 并以状态码 `1` 退出。`--help` 和 `--version` 使用普通文本。`serve` 的 stdout 专用于 MCP JSON-RPC，启动诊断写到 stderr。Windows helper 响应另有协议、会话与计时 `meta`；宿主复合动作按各自契约返回结果。

`--dry-run` 在不访问 VM 的情况下校验动作参数并预演；写动作的授权仍要满足。复合动作的预演不等于已经读取或验证 profile、同步源文件或 guest 状态。带策略的回收预演仍会校验本地策略文件。`serve` 不支持预演。

宿主子进程超时 `--timeout` 为 1–600 秒，默认 30 秒。stdout 上限为 8 MiB；UIA、任务、等待等动作还有独立预算。超时或取消只约束宿主等待，不保证 guest 工作停止或副作用撤销。

`outcome:"dispatched"` 表示动作已派发；`outcome:"unknown"` 表示不能据错误认定动作未发生。先重新查询应用或 VM 状态，再决定下一步。写操作不自动重试；工作流清理不等于恢复所有外部状态。

## 常见问题

- **找不到 Parallels CLI**：检查安装、授权和 `doctor`，必要时设置 `--prlctl` 或 `STARBRIDGE_PRLCTL`。
- **无法连接 Parallels Service**：检查服务和执行环境权限，不能据此认定未安装。
- **只有进程，没有窗口或控件**：核对登录用户、会话、锁屏、完整性级别和应用的 UIA 支持，按[桌面验收](DESKTOP_TESTING.md)对照已知夹具。
- **动作不存在或 helper 指纹不符预期**：更新同版本完整 helper 树，再读 `system.info`，不要只替换入口。
- **截图失败或画面无效**：先读 `screenshot.capabilities`，核对会话、窗口身份、后端和预算；后端支持不保证每个应用可捕获。
- **点击或输入失败**：先检查是否已出现副作用，再核对前台、输入桌面、遮挡、持键状态及明确后置条件，不直接重试。
- **MCP 工具缺失或授权失败**：核对启用开关、VM UUID、工具 grant、嵌套权限和 workspace；工具存在不等于对任意 VM 授权。

测试覆盖范围、合成数据要求和真实环境验收见[验证与验收](ACCEPTANCE.md)。
