# MCP 的 VM 与动作授权

默认配置保留现有本地行为：只读工具可以访问宿主账号可见的 VM；`--allow-vm-write` 额外开放四个生命周期工具。需要把 AI 限制在专用 VM 时，显式指定策略文件：

```sh
starbridge serve --access-policy /absolute/path/mcp-access-policy.json --allow-vm-write
```

复制 [策略示例](../examples/mcp-access-policy.json)，将示例 UUID 替换为 `starbridge vm list` 返回的实际 VM UUID：

```json
{
  "vms": {
    "11111111-1111-1111-1111-111111111111": ["vm_info", "guest_windows", "vm_start"]
  }
}
```

每个键必须是 UUID，大小写和花括号不影响匹配。允许的工具名为 `vm_info`、`guest_processes`、`guest_windows`、`guest_ui_tree`、`vm_start`、`vm_stop`、`vm_suspend`、`vm_resume`。写动作既需要策略授权，也需要 `--allow-vm-write`。完全没有获得授权的工具不出现在工具列表中。

`vm_list` 始终存在，只返回策略列出的 VM。空动作数组表示该 VM 仅在列表中可见；`{"vms":{}}` 拒绝访问所有 VM。未提供 `--access-policy` 时保留原行为。指定文件后，未知工具、非 UUID、重复字段、超过 64 KiB、文件读取失败等情况都会拒绝启动，并向 stderr 返回 `INVALID_ACCESS_POLICY`，不会退回无限制模式。策略在启动时加载，修改后须重启服务。

每次 VM 详情、guest 查询和生命周期操作都会重新读取宿主 VM 列表，再检查目标与动作。传入名称时，先在完整列表中解析唯一 UUID；名称重名、目标消失或返回结构无法确定 UUID 时拒绝访问。后续命令始终使用通过授权的 UUID，避免名称在检查后重绑定到另一台 VM。VM 列表身份字段支持 `uuid`、`ID`、`id`（不区分大小写）；多个字段冲突或重复 UUID 时失败关闭。库存查询错误保留错误码，但不向客户端转发可能包含其他 VM 信息的原始输出。

策略只约束该 MCP 实例，不限制用户直接运行普通 CLI，也不限制其他服务实例。它不是 macOS 账号权限隔离、跨进程租约或远程身份认证。
