# 受控 Windows 开发任务

任务动作运行当前 Windows 用户预先配置的程序，保留任务身份、状态和有界输出。`task.start`、`task.cancel`、`task.close` 需要显式 `--allow-write`；`task.status`、`task.output` 只读。先安装配套的 `developer-bridge.ps1` 和完整 `developer/` 目录。

## 配置一个合成任务

在 Windows 当前用户的 `%LOCALAPPDATA%\Starbridge\Developer\profiles.json` 配置任务。下面的 `synthetic-tests` 只输出固定字符串并以 0 退出，也可供[工作流示例](../examples/development-profile.json)使用；Windows 不在 `C:\Windows` 时调整路径。已有配置请合并 profile，勿直接覆盖整个配置文件。

```json
{
  "version": 1,
  "profiles": {
    "synthetic-tests": {
      "executable": "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
      "cwd": "C:\\Windows\\Temp",
      "args": ["-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "[Console]::Out.WriteLine('synthetic task'); exit 0"],
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

调用只能选择具名 profile，不能临时指定 exe、cwd 或脚本。可信配置可以选择解释器，上例的命令代码固定在本地配置中；`allow_extra_args:false` 防止请求追加解释器参数。环境覆盖仅允许 `allowed_env` 中的键。程序继承当前 Windows 用户环境，再应用 profile 的 `env` 和获准覆盖；这不是隔离环境或权限沙箱。

profile 超时默认 300000ms，范围 1000–3600000；请求只能缩短它。每条输出流默认最多保存 1 MiB，可配置 1024 字节至 16 MiB。GUI profile 设 `interactive:true`；`show_window` 默认跟随 interactive，也可显式设置。交互 profile 需要用户桌面，`task.close` 本身也要求交互会话。

guest 本地环境变量 `STARBRIDGE_DEV_HOME` 可改用另一个绝对状态目录，配置与 `tasks/` 随之迁移；请求不能指定状态路径。配置文件须为有界普通 JSON 文件，任务名为最多 64 个 ASCII 字符，匹配 `[A-Za-z0-9][A-Za-z0-9_.-]{0,63}`。

## 启动、等待和检查退出码

保存 `start.json`：

```json
{"profile":"synthetic-tests","env":{"BUILD_LABEL":"local-test"}}
```

```sh
starbridge --dry-run dev task.start VM --request start.json --allow-write
starbridge dev task.start VM --request start.json --allow-write > task-start-result.json
```

`task.start` 成功表示任务身份已记录，不保证应用已就绪或最终成功。保存实际返回的 `data.task_id`；后续请求只使用这个 ID。下面用 Python 生成 `task.json`：

```sh
python3 - <<'PY'
import json
from pathlib import Path
result = json.loads(Path('task-start-result.json').read_text())
if result.get('ok') is not True:
    raise SystemExit('task.start failed; inspect its result before retrying')
Path('task.json').write_text(json.dumps({'task_id': result['data']['task_id']}))
PY
starbridge dev task.status VM --request task.json
```

状态为 `starting`、`running`、`closing`、`cancelling`、`exited`、`cancelled`、`timed_out`、`failed` 或 `orphaned`。结果包含 profile、创建时间、PID、进程启动时间、直接子程序退出码和输出大小；尚无相关进程信息时为 null。状态保存在 guest，可跨宿主命令查询。需要最终结论时，先[等待结束，再断言退出码](DEVELOPER_WAIT.md)，不要只检查 task.start 或 `status:"exited"`。

请求附加参数最多 64 项，每项最多 1024 Unicode 码点；环境覆盖最多 32 项，每值最多 2048 码点，均不允许 NUL。任务规范化请求另有 12000 字节预算。启动前按实际 Windows 参数引用规则检查 exe、固定参数、额外参数组成的完整命令行：最多 32767 个 UTF-16 代码单元，含终止 NUL 及引号/转义扩张。超限返回 `TASK_COMMAND_LINE_TOO_LONG`，不创建任务目录或 worker。

## 输出与停止

保存输出请求；ID 必须替换成实际 task_id：

```json
{"task_id":"0123456789abcdef0123456789abcdef","stream":"stdout","offset":0,"limit":16384}
```

```sh
starbridge dev task.output VM --request output.json
starbridge dev task.cancel VM --request task.json --allow-write
starbridge dev task.close VM --request task.json --allow-write
```

`stream` 为 stdout 或 stderr。输出按字节存储，每次返回 `data_base64`、`offset`、`next_offset`、`eof`、`truncated`；默认读取 16384 字节，单次最多 65536。继续使用 `next_offset`，先拼接字节再按应用编码解码，避免把分块 UTF-8 当独立字符串。`eof:true` 要求任务结束且本流已读完；达到落盘上限后继续排空但丢弃多余字节，标记 truncated。

worker 在进程暂停时将其加入非 breakaway、kill-on-close 的 Windows Job Object，再恢复执行。取消和任务超时针对该任务拥有的进程树，不接受任意 PID。控制请求核对原 worker 的 PID、开始时间和程序路径；失去 worker 身份的记录为 orphaned。worker 意外结束时，其 Job Object 关闭会终止所拥有的进程。

`task.close` 只请求主窗口正常关闭。无主窗口或关闭受阻时为 `accepted:false, control_status:"blocked"`，不会升级为强制终止；accepted 只表示请求已发送，仍需查询最终状态。控制状态还可能是 pending 或 already_finished。宿主取消一次请求不等于取消后台任务，超时/中断后先查询原任务，勿盲目重复 task.start。

响应不回显环境值，预演隐藏附加参数与环境值；应用日志仍可能自己打印这些内容。worker 读取执行参数后删除临时请求；启动或身份记录失败时入口尝试删除请求，并终止尚未恢复的 worker。身份、状态和有界日志会保留，没有自动保留期限或清理动作。

Windows 合成测试仅创建自己的临时配置、exe 与进程树：

```powershell
& .\tests\windows\developer-tasks.ps1
```

它不连接 Parallels 或操作已有应用；真实 VM、GUI 与传输行为另见[开发验收](DEVELOPER_ACCEPTANCE.md)。
