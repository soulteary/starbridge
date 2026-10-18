# MCP 的 VM 与动作授权

未配置策略时，服务可访问宿主账号可见的全部 VM；默认工具只读，`--allow-vm-write` 另开放启动、关机、挂起、恢复。需要限定 VM 和动作时显式指定策略：

```sh
starbridge serve --access-policy /absolute/project/mcp-access-policy.json --allow-vm-write
```

从 `starbridge vm list` 取得目标 UUID，复制[策略示例](../examples/mcp-access-policy.json)，再保存为本地策略文件：

```json
{"vms":{"11111111-1111-1111-1111-111111111111":["vm_info","guest_windows","vm_start"]}}
```

`vms` 的键必须是 UUID，不能是显示名称；匹配忽略大小写和成对花括号。空数组表示仅能列出该 VM，`{"vms":{}}` 不允许访问任何 VM。

## 可授予的能力

基础动作名为 vm_info、guest_processes、guest_windows、guest_ui_tree、vm_start、vm_stop、vm_suspend、vm_resume。`vm_list` 始终提供，只列出策略中的 VM，不写入 grant 数组。

启用 `--developer-tools` 后，还能调用 doctor_vm、vm_snapshot_list 和宿主已编译的 `dev.<action>` 工具。两个写开关独立：VM 写工具需要 `--allow-vm-write`，开发写工具需要 `--allow-guest-write`；工作流可能同时需要两者。授予工具名不会自动开启相应开关。

服务仅列出至少获一个 VM 授权、且满足开关/后端/workspace 条件的工具。工具出现在列表中，不表示可对任意 VM 使用。策略可包含当前编译版本识别但尚未启用的工具 grant；启用前不会开放调用。

工作流、等待、证据和视觉动作还检查实际内层能力，详细示例见[MCP 开发工具](MCP_DEVELOPER.md#嵌套动作授权)。grant 不是 Windows 提权：交互会话、进程权限、guest 文件根目录及应用状态仍须满足。

## 读取与执行

策略在服务启动时加载，修改后需重启。文件必须是最多 64 KiB 的普通文件，不接受符号链接或命名管道；读取失败、未知工具、重复工具、重复键、规范化后重复 UUID 和其他无效配置均以 INVALID_ACCESS_POLICY 拒绝启动，诊断写入 stderr。

获准执行前会重新读取 VM 列表，核对目标与权限。名称必须唯一解析为 UUID，实际命令使用该 UUID；目标消失、重名、重复 UUID 或身份字段冲突时拒绝。能从明确 UUID 和 grant 直接判定的拒绝可提前结束，不必查询清单。清单读取失败保留错误码，隐藏可能包含其他 VM 信息的原始输出。

策略只约束当前 MCP 实例，不限制普通 CLI、其他服务或账号可直接运行的程序；不提供 macOS 账号隔离、远程认证或网络服务入口。宿主路径作用域与并发行为分别见[开发工具配置](MCP_DEVELOPER.md#宿主工作目录)、[并发与取消](MCP_CONCURRENCY.md)。
