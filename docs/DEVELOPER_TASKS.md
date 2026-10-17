# 受控 Windows 开发任务

安装配套的 `developer-bridge.ps1` 和完整 `developer/` 目录后，通过 `dev` 入口调用任务。任务启动、取消和正常关闭要求显式 `--allow-write`，状态与输出查询只读。

## 配置具名任务

在 Windows 当前用户的 `%LOCALAPPDATA%\Starbridge\Developer\profiles.json` 配置可执行程序、工作目录和固定参数。示例中的路径需替换为自己的项目目录：

```json
{
  "version": 1,
  "profiles": {
    "test": {
      "executable": "C:\\Project\\bin\\tests.exe",
      "cwd": "C:\\Project",
      "args": ["--report", "results.json"],
      "allow_extra_args": false,
      "env": {},
      "allowed_env": ["BUILD_LABEL"],
      "timeout_ms": 300000,
      "max_output_bytes": 1048576,
      "interactive": false,
      "show_window": false
    }
  }
}
```

请求只能选择已配置的 profile；不能临时指定 exe、工作目录或 shell。额外参数默认禁止，环境覆盖仅允许 `allowed_env` 中的键；请求超时只能缩短 profile 的预算。GUI profile 可设置 `interactive:true`、`show_window:true`。这些配置限定工具入口，应用仍以当前 Windows 用户身份运行。

启动前会用实际 Windows 参数引用规则计算可执行程序、固定参数和额外参数的完整命令行长度。上限为 32767 个 UTF-16 代码单元，包含末尾终止空字符；引号及转义产生的字符也计入。超限返回 `TASK_COMMAND_LINE_TOO_LONG`，不会创建任务目录或启动 worker。

状态目录默认位于同一用户目录的 `tasks/` 下。guest 本地环境变量 `STARBRIDGE_DEV_HOME` 可显式选择另一绝对目录，主要用于隔离测试；请求不能指定状态路径。

## 启动与观察

保存 `start.json`：

```json
{"profile":"test","env":{"BUILD_LABEL":"local-test"}}
```

```sh
starbridge dev task.start VM-UUID --request start.json --allow-write
```

保存返回的 `data.task_id`，后续请求均使用它：

```json
{"task_id":"返回的32位任务ID"}
```

```sh
starbridge dev task.status VM-UUID --request task.json
starbridge dev task.cancel VM-UUID --request task.json --allow-write
starbridge dev task.close VM-UUID --request task.json --allow-write
```

`status` 为 `starting`、`running`、`closing`、`cancelling`、`exited`、`cancelled`、`timed_out`、`failed` 或 `orphaned`。结果保留 profile、创建时间、PID、进程开始时间、退出码和输出大小；进程尚未创建时相关字段为 `null`。退出码来自配置的直接子程序。状态与结果保存在 guest，可跨宿主命令查询。

worker 在进程暂停期间将它加入 Windows Job Object，随后才恢复运行。取消和超时作用于该任务拥有的进程树；控制请求核对 worker 的 PID、开始时间与程序路径，不对任意 PID 调用强制停止。worker 意外结束时 Job Object 关闭会终止其拥有的进程；失去原 worker 身份的记录报告 `orphaned`。

`task.close` 只请求正常关闭主窗口。没有主窗口或关闭被阻止时返回 `accepted:false`、`control_status:blocked`，不会升级为强制终止。`accepted:true` 表示关闭请求已发送，完成与否继续查询状态。已经结束的任务返回 `already_finished`；控制请求尚未确认时返回 `pending`。

## 分块读取输出

```json
{"task_id":"返回的32位任务ID","stream":"stdout","offset":0,"limit":16384}
```

```sh
starbridge dev task.output VM-UUID --request output.json
```

输出按字节存储，`stream` 可选 `stdout` 或 `stderr`。每次返回 `data_base64`、`offset`、`next_offset`、`eof` 和 `truncated`；客户端解码 Base64 并累计字节，再按应用实际编码处理文本。继续使用返回的 `next_offset`，单次最多 65536 字节。输出可在任务运行期间查询；`eof:true` 表示任务已结束且本流已读完。

每条输出流按 profile 的 `max_output_bytes` 有界落盘，默认 1 MiB，最大 16 MiB；超过后继续排空但丢弃多余字节，并标记 `truncated:true`。响应不回显环境值，预演会隐藏附加参数与环境值。应用自己写到 stdout/stderr 的内容仍属于任务日志。应用启动后即删除含执行参数与环境的临时请求文件，保留身份、状态和有界输出供后续查询；需要保留期限时由使用者清理已完成任务目录。

worker 启动或身份记录失败时，入口会删除临时请求，并终止尚未恢复运行的 worker。

## 验证

宿主 fake 测试验证授权、输入、参数传输、响应身份、输出预算和未知超时结果。Windows 合成任务验收创建自己的临时 exe、配置和进程树：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "& '.\tests\windows\developer-tasks.ps1'"
```

该脚本不连接 Parallels，也不操作已有应用。真实 VM 的会话、GUI 与传输行为另按[桌面验收](DESKTOP_TESTING.md)检查。
