# 调试证据导出

`evidence.capture` 把显式选择的应用观察保存到宿主目录，并生成带 SHA-256 的清单。观察本身只读，但创建本机资料需要 `--allow-write`。运行时目标必须是 VM UUID。

保存 `evidence.json`；示例 PID 42 需替换为目标应用的实际 PID：

```json
{"output_directory":"./artifacts","observations":[{"name":"fixture-process","action":"processes.list","params":{"pid":42,"limit":1}},{"name":"fixture-window","action":"windows.list","params":{"pid":42,"limit":10}}]}
```

```sh
starbridge --dry-run dev evidence.capture VM-UUID --request evidence.json --allow-write
starbridge dev evidence.capture VM-UUID --request evidence.json --allow-write
```

CLI 相对输出路径以宿主当前目录为基准；MCP 需要 workspace 内的绝对路径。预演不创建目录、不执行观察，只校验外层结构与收集范围并隐藏输出目录；不会验证每个 guest 查询的实际可用性。

## 选择观察

每次 1–16 项，名称唯一，匹配 `[a-z][a-z0-9_-]{0,31}`，不能使用保留名称 manifest。支持集合为 `system.info`、`processes.list`、`windows.list`、`ui.tree`、`ui.query`、`task.status`、`task.output`、`screenshot.capture`；需要安装相应模块。`ui.observe` 不在该集合中，图片导出请使用 screenshot.capture。

进程、窗口列表必须按 PID 限定；UIA 与截图参数沿用各自窗口身份要求。task.output 只保存请求指定的一块流数据，不会自动拉取整份任务日志。需要检查全部输出时，先按 next_offset 分块读取，或明确选择需要留存的片段。

## 文件、预算与部分结果

每次在输出目录下创建随机 run_id 子目录；新目录为 0700、文件为 0600，已有父目录权限不重置，已有文件不覆盖。成功结果返回 `run_id`、`manifest_file`、已记录项数和 `complete`。

每项保存独立 JSON；截图 Base64 解码成 PNG，任务输出解码成原始字节 `.log`，JSON 中改为文件引用，不修改原始调用结果。PNG 会完整解码校验，仍受截图动作的字节、像素、身份和时限约束；最多 4 MiB、16 Mi 像素。单个证据文件上限 8 MiB，日志块最多 64 KiB。

整个运行最多 32 MiB，含 manifest.json；清单预留 64 KiB。程序先准备一项的全部 PNG、日志和格式化 JSON，按实际字节检查预算，再创建文件。预算不足时不会留下该项的一部分图片或日志，会停止后续观察，并记录 `stop_reason:"budget_exhausted"`。写入失败或取消也会停止；普通查询失败则记录其错误，仍可继续后续观察。

manifest.json 记录目标 UUID、Starbridge 版本、时间、各项状态/耗时、文件大小与哈希，以及 requested_observations、complete、stop_reason。`bytes` 包含清单自身。`ok:true` 只表示导出成功返回，**不等于所有观察成功**：必须检查 `complete` 和清单中的每项结果。`complete:false` 可以表示观察失败、预算不足、写入失败或取消；哈希也只核对资料内容，不验证应用业务状态。

证据可能包含窗口文字、控件值、截图、任务 stdout/stderr 和错误诊断；不会自动匿名化、加密、上传或定期删除。按项目需要缩小观察范围，保存后检查内容，再决定是否分享或提交仓库。MCP 还要求导出工具及各被选观察工具的 grant，见[MCP 开发工具](MCP_DEVELOPER.md#嵌套动作授权)。

自动测试使用合成结果、生成 PNG 与固定字节，验证完整 PNG、预算、哈希、部分结果和文件权限；不读取真实桌面或私人日志。真实采集另见[开发验收](DEVELOPER_ACCEPTANCE.md)。
