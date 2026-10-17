# 项目开发工作流

`workflow.run` 按 JSON profile 顺序执行同一 VM 的开发步骤，并逐步保存结果。它适合把文件准备、具名任务、等待、断言和证据采集串起来；即使步骤全部只读，也需要 `--allow-write`，因为运行会创建宿主文件。

## 运行一个项目

先配置 Windows 中的[具名任务](DEVELOPER_TASKS.md)，再复制[工作流示例](../examples/development-profile.json)。示例使用合成 VM UUID 和 `synthetic-tests` 任务名，必须将 UUID 替换为实际 VM，并在 guest 安装相应模块、配置该任务。

保存 `workflow-request.json`：

```json
{"profile_file":"/absolute/project/development-profile.json","output_directory":"/absolute/project/runs"}
```

```sh
starbridge dev workflow.run VM-UUID --request workflow-request.json --allow-write
```

示例依次启动 VM、读取环境、启动任务、等待 `exited`，再断言退出码为 0，最后执行显式停止式回收。它不会自动配置 guest task profile，也不会执行请求中任意 shell 字符串。

profile 顶层字段：

- `version:1`、`vm` UUID、1–32 个 `steps`；每步包含唯一 `name`、固定 `action` 和可选 `params`。名称匹配 `[a-z][a-z0-9_-]{0,31}`。
- 可选 `timeout_seconds`：1–600 覆盖每条宿主命令的超时；省略或 0 沿用全局 `--timeout`。它不是整个工作流的总期限，等待动作另有自身预算。
- 可选 `developer_script`：CLI 使用的绝对 Windows helper 路径；MCP 禁止 profile 覆盖服务的脚本设置。
- 可选 `cleanup`：只能是显式 `vm.recycle`，模式为 `stop`、`restore` 或 `delete`；省略时没有自动清理。

CLI 的 profile 和输出路径可以是相对路径，均相对于宿主当前工作目录；步骤里的宿主文件路径也不会自动改为相对 profile 的位置。建议使用绝对路径，避免从其他目录执行时选错文件。MCP 要求它们位于显式 workspace 内，见[MCP 开发工具](MCP_DEVELOPER.md)。

加载 profile 时拒绝非普通文件、超过 64 KiB、无效 UTF-8、孤立代理转义、重复 JSON 键、未知结构字段、重名步骤、递归工作流和未注册动作。中文、emoji 与合法代理对可用。各动作 params 在实际执行前交给对应校验器；加载 profile 成功不代表所有 guest 条件已满足。

`--dry-run` 只校验外层请求并脱敏路径，不读取 profile、不创建输出、不访问 VM。因此它不能替代 profile 加载与实际验收。

## 步骤与结果引用

CLI profile 可使用基础查询、VM 启停/克隆/快照/回收及编译进程序的开发动作；MCP 的可用 VM 写操作更少。所有步骤固定面向 profile 的同一 VM，调用 VM 必须与 profile UUID 一致。即使某步克隆出另一 VM，后续步骤仍操作原 VM。

用只含 `$ref` 的对象引用已完成步骤的结果，不做字符串插值：

```json
{"name":"wait","action":"wait.until","params":{"action":"task.status","params":{"task_id":{"$ref":"run.data.task_id"}},"predicate":{"path":"data.status","op":"eq","value":"exited"},"timeout_ms":60000}}
```

上例假设前面名为 `run` 的步骤成功启动任务。路径从该步骤的结果 envelope 开始，支持点分隔字段和数组下标。缺失/前向引用会使当前步骤失败。引用用于普通步骤 params；cleanup 不解析这些引用。MCP 还拒绝整组 params 或宿主路径通过动态引用产生。

## 检查点、失败与清理

每次运行在 `output_directory` 下创建随机 `run_id` 目录，使用 0700 目录和 0600 文件；已有父目录权限不改变。每步写入 `01-name.json` 等检查点，最后写入 `report.json`，记录 profile SHA-256、步骤耗时/结果、失败步骤和清理状态。报告不复制 profile 输入，但动作输出仍可能包含 UI 文本、图片 Base64 或应用日志，须作为敏感调试材料保存。

步骤失败后不再执行下一步，不重试写动作；已经产生的 VM、文件或应用变化不会回滚。只要上下文仍可用，配置的 cleanup 会在成功或失败后执行。上下文已取消时，清理保持 `pending`；最后一步结束后的取消也不会报告工作流成功。

需要稍后单独清理时，检查原报告，再显式调用：

```sh
starbridge dev workflow.cleanup VM-UUID --request workflow-request.json --allow-write
```

这会创建新运行，只执行 profile 的 cleanup，不恢复原步骤。它仍重新加载并校验 profile，不是读取旧报告后自动恢复。

快照恢复及恢复/删除式回收要求外层请求的 `confirm_vm` 与命令中的 VM 字符串完全一致。字面声明的破坏性步骤会在首步前检查确认，底层操作执行时还会核对。清理失败也会使工作流失败。失败返回 `WORKFLOW_FAILED`，详情给出 `run_id`、`report_file`、`failed_stage`、`cleanup`；检查报告中的原始步骤结果，尤其是 `outcome:"unknown"`，再决定下一步。

MCP 需要 `dev.workflow.run` 或 `dev.workflow.cleanup`，以及实际执行步骤和嵌套观察的 grant。单独 cleanup 只要求清理操作权限与路径，不要求未执行步骤的授权；MCP 只允许停止式回收。客户端总超时应覆盖整个运行，而不是只覆盖单条命令。

宿主测试使用 fake 命令、合成 UUID 和临时文件，验证引用、Unicode/重复键校验、取消、检查点与确认规则；不代表已验证真实 VM 或桌面行为。
