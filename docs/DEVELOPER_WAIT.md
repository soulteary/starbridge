# 条件等待与单次断言

`wait.until` 重复执行一个显式只读观察，直到条件成立；`assert.matches` 只观察一次。两者不启动程序、不重试输入或其他写操作，不需要 `--allow-write`。

## 等待应用或任务

将下面内容保存为 `wait.json`；示例只寻找名为 `SyntheticFixture` 的进程：

```json
{"action":"processes.list","params":{"name":"SyntheticFixture","limit":10},"predicate":{"path":"data.processes","op":"nonempty"},"timeout_ms":30000,"interval_ms":250}
```

```sh
starbridge --dry-run dev wait.until VM --request wait.json
starbridge dev wait.until VM --request wait.json
```

观察动作是固定集合：`vm.info`、`system.info`、`processes.list`、`windows.list`、`ui.tree`、`ui.query`、`task.status`。`vm.info` 的 params 只能是 `{}`；其他参数沿用各动作契约，包括 UI 查询的完整窗口身份。截图、`ui.observe` 和写动作不在等待集合中。`wait.until` 与 `assert.matches` 在预演和运行前都会校验被观察动作的参数。

等待任务结束时，用 `task.start` 实际返回的 ID 替换示例 ID：

```json
{"action":"task.status","params":{"task_id":"0123456789abcdef0123456789abcdef"},"predicate":{"path":"data.status","op":"eq","value":"exited"},"timeout_ms":60000}
```

`exited` 只表示程序退出；随后用单次断言检查退出码。例如保存为 `assert.json`：

```json
{"action":"task.status","params":{"task_id":"0123456789abcdef0123456789abcdef"},"predicate":{"path":"data.exit_code","op":"eq","value":0}}
```

```sh
starbridge dev assert.matches VM --request assert.json
```

`assert.matches` 不匹配返回 `ASSERTION_FAILED`；查询自身失败则保留对应错误。它接受相同参数结构，但 `timeout_ms/interval_ms` 不引入重试，也不覆盖单次查询的宿主命令超时。

## 条件与时间预算

`predicate.path` 从整个观察 envelope 开始，用点分隔字段和数组下标，如 `data.items.0.is_enabled`；不支持通配符或表达式。路径最长 256 字节，数组下标最多 4 位数字。

- `exists`：路径存在且值不是 null，不接受 `value`。
- `empty` / `nonempty`：检查字符串、数组、对象的长度，不接受 `value`。
- `eq`：要求一个 JSON 标量 `value`，可以是 null；数值按数值比较，不要求相同数字写法。
- `contains`：要求字符串 `value`，按大小写敏感的子串匹配。

缺失路径始终不匹配。`timeout_ms` 默认 30000，范围 1–600000；`interval_ms` 默认 250，范围 10–10000，是每次观察结束后的等待时间，不是固定采样频率。单次观察还受宿主 `--timeout` 和总等待截止时间约束。

仅部分暂时错误会继续等待，例如 VM 命令失败、窗口尚未出现、交互会话尚不可用、UIA 超时；无效参数、身份/契约问题等会直接结束，不会把所有错误都当成“应用未就绪”。耗尽等待时间返回 `WAIT_TIMEOUT`，主动取消返回 `INTERRUPTED`，详情包含 `attempts`、`last_error_code`、`elapsed_ms` 和 `outcome:"no_result"`。成功返回 `matched:true`、尝试次数、耗时和最后一次观察。

VM 已启动、guest 可用、进程出现、窗口出现、业务结果完成是不同条件，应按调试任务分别验证。MCP 调用还需外层等待/断言工具及被观察工具的 grant，见[开发工具授权](MCP_DEVELOPER.md#嵌套动作授权)。
