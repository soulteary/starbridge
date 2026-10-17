# MCP 的 VM 与动作授权

默认只读工具可访问宿主账号可见的所有 VM；`--allow-vm-write` 另开放启动、关机、挂起和恢复。限制 VM 与工具权限时，指定策略文件：

```sh
starbridge serve --access-policy /absolute/path/mcp-access-policy.json --allow-vm-write
```

复制 [策略示例](../examples/mcp-access-policy.json)，用 `starbridge vm list` 查到的 UUID 替换占位值：

```json
{
  "vms": {
    "11111111-1111-1111-1111-111111111111": ["vm_info", "guest_windows", "vm_start"]
  }
}
```

`vms` 的键必须是 UUID，匹配时忽略大小写和成对花括号。可授权的工具为 `vm_info`、`guest_processes`、`guest_windows`、`guest_ui_tree`、`vm_start`、`vm_stop`、`vm_suspend`、`vm_resume`。

- `vm_list` 始终提供，只列出策略中的 VM。空动作数组表示仅允许列出该 VM；`{"vms":{}}` 不允许访问任何 VM。
- 其余工具仅在策略中获授权后列出，写操作还需 `--allow-vm-write`。
- 策略在启动时加载，修改后需重启。文件上限为 64 KiB；读取失败、未知工具、无效或重复配置均以 `INVALID_ACCESS_POLICY` 拒绝启动，错误写入 stderr。

每次操作前都会重新查询 VM 列表并检查权限。名称须能解析为唯一 UUID，实际命令使用该 UUID；重名、目标消失或身份信息冲突时拒绝访问。列表查询失败时保留错误码，不转发可能包含其他 VM 信息的原始输出。

策略只约束当前 MCP 实例，不限制普通 CLI 或其他服务实例；它不提供 macOS 账号隔离或远程身份认证。
