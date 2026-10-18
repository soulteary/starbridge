# MCP 开发工具

默认 `serve` 提供 5 个只读工具，`--allow-vm-write` 增加启动、关机、挂起和恢复。开发动作必须显式开启：

```sh
starbridge serve --developer-tools
starbridge serve --developer-tools --allow-guest-write \
  --developer-workspace /absolute/project --access-policy /absolute/project/mcp-access.json
```

`--allow-guest-write` 开放注册为写操作的开发工具，也包括下载、证据和工作流等宿主写入；它不授权 VM 操作。工作流中的 VM 启停还需 `--allow-vm-write`。两个写开关互不包含；`--allow-guest-write`、`--developer-workspace` 必须与 `--developer-tools` 同用。

客户端可参考[基础配置](../examples/codex-mcp.toml)，按需要改为：

```toml
[mcp_servers.starbridge]
command = "/absolute/project/bin/starbridge"
args = ["serve", "--developer-tools", "--allow-guest-write", "--developer-workspace", "/absolute/project", "--access-policy", "/absolute/project/mcp-access.json"]
startup_timeout_sec = 20
tool_timeout_sec = 180
```

按客户端支持的配置格式使用，并为完整工作流设置足够的调用期限；示例不开放 VM 写入。此配置不会自动复制到客户端或安装 Windows helper。

## 工具发现与调用

开发模式增加 `doctor_vm`、`vm_snapshot_list`，并为原工具补充 `guest_processes.include_path`、`guest_windows.include_hidden`、`guest_ui_tree.timeout_ms`。`doctor_vm` 检查 readiness，不启动 VM；成功返回检查结果不表示全部就绪项通过。

开发动作按宿主注册表暴露为 `dev.<action>`，如 `dev.system.info`、`dev.task.start`、`dev.input.text`、`dev.screenshot.capabilities`、`dev.ui.observe`。工具列表取决于编译版本、后端接口、服务开关、workspace 与访问策略；Windows 端也必须安装配套模块，用 system.info 查看 guest capabilities。注册列表不是当前桌面可用性的保证。

每个开发工具接受 vm 和可选 params 对象，拒绝其他顶层字段；这与 CLI 请求文件直接存参数对象不同：

```json
{"vm":"11111111-1111-1111-1111-111111111111","params":{}}
```

params 由动作校验器规范化，省略相当于 `{}`；最终请求最多 16000 UTF-8 字节。原始参数拒绝无效 UTF-8、孤立代理转义，合法中文和 emoji 可用。未知本地主机动作在没有明确执行/路径策略前不会开放，不能通过注册任意 Execute 绕过 workspace。

成功与失败沿用[开发协议](DEVELOPER.md)，MCP `isError` 对应 `ok:false`；文本与 structured content 表示同一元数据。截图、ui.observe、ui.click 的成功响应另附原生 image/png，不在两份元数据中重复 Base64；返回前核对完整 PNG、尺寸、哈希和本次预算。失败保持普通 envelope；离线预演使用 CLI `--dry-run dev`，`serve` 不接受 `--dry-run`。

`input.text` 在预演和错误处理中隐藏文本；`ui.click`、input.text 已提交后的无效响应也可能是 unknown。输入派发不代表业务成功，视觉流程的 verification 保持 pending；查看[UI 操作](DEVELOPER_UI_ACTIONS.md)与[视觉流程](DEVELOPER_VISUAL.md)。

## 嵌套动作授权

[访问策略](MCP_ACCESS.md) 按 VM UUID 和工具名逐项授权；开启开发模式不增加 grant。外层工具不能授权任意子动作：

- wait.until / assert.matches：需要自己的 dev.* grant 和被观察工具的 grant，如 dev.task.status 或 guest_windows。
- evidence.capture：需要 dev.evidence.capture 及每项观察工具的 grant。
- workflow.run：需要 dev.workflow.run、各步骤及其嵌套观察的 grant；VM 写步骤还需 VM 写开关。workflow.cleanup 只要求自身及实际 cleanup 的 grant/路径，不要求未执行步骤的权限，仍校验整个 profile 的结构和 VM。
- ui.observe：需要 dev.ui.observe、dev.screenshot.capture；默认 include_uia:true 还需 dev.ui.query。
- ui.click：需要 dev.ui.click、dev.screenshot.capture、dev.input.mouse；默认 include_uia:true 还需 dev.ui.query，focus:true 再需 dev.window.focus，不要求 dev.ui.observe。工作流中无法静态判定的 focus 引用会保守要求聚焦权限；include_uia:false 才能省去 UIA grant。

高层 files.upload/download/sync 的 grant 授权其完整有界传输，不另要求每个内部 file.* RPC grant；直接调用低层 file.* 则需要对应工具 grant。bootstrap 动作使用固定 loader，权限由其自身 grant 和路径检查控制。

例如，运行示例工作流并停止 VM，可使用以下策略；还需同时开启两个写开关与 workspace：

```json
{"vms":{"11111111-1111-4111-8111-111111111111":["dev.workflow.run","dev.workflow.cleanup","vm_start","vm_stop","dev.system.info","dev.task.start","dev.wait.until","dev.assert.matches","dev.task.status"]}}
```

工作流 profile 的 UUID 必须与调用 VM 一致，不能覆盖服务的 developer_script。MCP 不开放克隆、快照创建/恢复、删除 VM，以及 restore/delete 式回收；工作流停止式 vm.recycle 映射到 vm_stop。CLI 仍可按其确认规则使用这些操作。

## 宿主工作目录

`--developer-workspace` 必须指向已存在的目录，目录本身不能是符号链接。未配置时，工具列表不包含 files.upload/download/sync、bootstrap.install、evidence.capture、workflow.run/cleanup。bootstrap.rollback 不读取宿主文件，因此不依赖 workspace。

宿主输入/输出必须是作用域内的干净绝对路径，不允许 `..`、作用域内符号链接或不符合类型的文件。具体字段是 local_path、同步 profile/source、安装 source_dir、证据 output_directory、工作流 profile_file/output_directory；输出可以尚不存在，动作自身的父目录要求仍适用。Windows 的 root/path、target_dir、任务参数、谓词字段和观察 token 不当作宿主路径。

同步 source 相对同步 profile 解析，整个来源树接受检查；安装树也检查文件类型与路径。工作流在调用 guest 前递归检查实际步骤、证据项及同步 profile。宿主路径、整组 params 和动态动作名不能来自 `$ref`；任务 ID 等 guest 值可按工作流规则引用。

作用域内 profile 读取拒绝非普通文件、symlink、超 64 KiB、无效 Unicode JSON、重复键或无法静态核对的结构。授权失败和作用域预检查失败不会执行 guest 命令；明确 UUID 且 grant 不足时，也无需枚举 VM。

同一服务实例的宿主文件动作串行，锁覆盖路径预检查和完整调用；后端并发与 VM/Windows 会话锁见[并发与取消](MCP_CONCURRENCY.md)。workspace 是路径限制，不是进程沙箱；同一账号的其他进程仍能修改文件，guest 根目录策略也须单独配置。
