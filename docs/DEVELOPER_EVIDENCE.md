# 调试证据

`dev evidence.capture VM-UUID --request evidence.json --allow-write` 将显式选择的应用观察保存到本机新建的随机运行目录。写许可用于创建本机资料，观察本身只读。

```json
{"output_directory":"./artifacts","observations":[{"name":"fixture-process","action":"processes.list","params":{"pid":42,"limit":1}},{"name":"fixture-window","action":"windows.list","params":{"pid":42,"limit":10}}]}
```

每次接受 1–16 个唯一名称的观察。支持 `system.info`、`processes.list`、`windows.list`、`ui.tree`、`ui.query`、`task.status`、`task.output`、`screenshot.capture`；需要安装被选择的模块。进程和窗口观察必须按 PID 限定，避免收集无关应用。参数遵循对应动作契约。

每项保存独立 JSON；截图的 `png_base64` 解码为 PNG，任务输出的 `data_base64` 按字节保存为日志。JSON 改为引用生成的文件，原始调用结果不会被修改。观察项名称不能使用为清单保留的 `manifest`。单个文件最多 8 MiB，PNG 最多 4 MiB / 6400 万像素，日志块最多 64 KiB；实际截图还受截图动作自身的更严格限制。

整个运行最多 32 MiB，包含 `manifest.json`。导出先准备一项的全部图片、日志和格式化 JSON，按实际字节数检查预算，再创建文件；为清单预留 64 KiB。预算不足时不会写入该项的部分图片或日志，会停止后续观察，并在部分清单中记录 `stop_reason:"budget_exhausted"`。清单的 `bytes` 是所有已登记文件及清单自身的总字节数。

`manifest.json` 包含版本、目标 UUID、时间、每项耗时、状态、文件大小与 SHA-256。目录和文件使用仅当前账号可访问的权限创建，已有文件不会覆盖。`complete:false` 表示至少一项失败、预算不足或调用被取消；请检查清单，不能将部分证据视为全部检查通过。捕获失败的查询也会保存错误，便于区分宿主、guest 与目标应用问题。

证据包含选定应用的界面和日志内容，应保存在项目指定目录；自动测试只使用合成进程、生成图片和固定字节串，不读取本机桌面或私人日志。
