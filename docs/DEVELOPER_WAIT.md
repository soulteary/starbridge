# 条件等待与断言

`dev wait.until VM --request wait.json` 重复执行只读查询，直到显式条件成立。它不重试启动、输入或其他写操作，也不会修改 VM。

```json
{"action":"processes.list","params":{"name":"SyntheticFixture","limit":10},"predicate":{"path":"data.processes","op":"nonempty"},"timeout_ms":30000,"interval_ms":250}
```

观察动作支持 `vm.info`、`system.info`、`processes.list`、`windows.list`、`ui.tree`、`ui.query` 和 `task.status`；后两者需要安装对应开发模块。`vm.info` 不接受 params。其余参数沿用被查询动作的契约。

`path` 使用点分隔的字段和数组下标，例如 `data.nodes.0.is_enabled`。`op` 支持 `exists`、`empty`、`nonempty`、`eq` 和字符串 `contains`；后两者需要标量 `value`。缺失路径不匹配，`exists` 还要求值非 null。`empty` / `nonempty` 只用于字符串、数组和对象。

`timeout_ms` 为 1–600000，默认 30000；`interval_ms` 为 10–10000，默认 250。超时返回 `WAIT_TIMEOUT`，取消返回 `INTERRUPTED`，附尝试次数、最后错误码和耗时。成功返回本次观察及匹配信息。可用 `--dry-run` 检查计划，此时不连接 Parallels。

相同参数也可用于 `dev assert.matches VM --request condition.json`。此动作只查询一次，条件不匹配即返回 `ASSERTION_FAILED`，适合检查任务退出码等最终结果；`timeout_ms` / `interval_ms` 不会引入重试或改变单次查询的宿主超时。

准备应用时可依次等待 guest 可用、目标进程出现、窗口出现、特定控件启用或值变化。VM 命令成功、单次 doctor 通过和目标应用就绪是不同检查条件；应按项目需要明确选择。
