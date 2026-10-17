# MCP 开发工具

默认 `serve` 保持 5 个只读工具，`--allow-vm-write` 增加 4 个基础 VM 操作。开发工具需要另行开启：

```sh
starbridge serve --developer-tools
starbridge serve --developer-tools --allow-guest-write \
  --developer-workspace /absolute/project --access-policy /absolute/mcp-access.json
```

`--allow-guest-write` 开放注册为写操作的开发动作，包括写宿主文件的动作；它不授权 VM 操作。工作流中的启动、关机、挂起、恢复另需 `--allow-vm-write`。两个开关都不隐含另一个。`--allow-guest-write`、`--developer-workspace` 必须与 `--developer-tools` 同用。

开发模式增加 `doctor_vm`、`vm_snapshot_list`，并为原工具增加 `guest_processes.include_path`、`guest_windows.include_hidden` 和 `guest_ui_tree.timeout_ms`。后端需要实现对应的可选接口，旧 Backend 无需修改。

编译进主机程序的开发动作按注册表暴露为 `dev.<action>`，例如 `dev.system.info`、`dev.task.start`、`dev.ui.query`。Windows 中也必须安装对应模块；用 `system.info` 核对实际安装能力。没有开启写授权时，开发写工具不会列出；缺少文件策略的未来本地主机动作同样不会开放。工具的读写和交互提示取自注册信息。

每个开发工具接受 `vm` 和可选 `params` 对象；不接受其他顶层字段。`params` 由该动作的严格校验器处理，省略相当于 `{}`：

```json
{"vm":"11111111-1111-1111-1111-111111111111","params":{}}
```

成功、失败及取消使用同一 [开发协议](DEVELOPER.md) envelope；MCP 的文本内容与结构化内容一致，`isError` 对应 `ok:false`。写操作超时不代表已撤销，先查询实际状态。

`dev.screenshot.capture`、`dev.ui.observe` 和 `dev.ui.click` 成功时额外返回原生 MCP PNG 图片，客户端可直接显示给视觉模型；文本和结构化结果保留坐标、时间、尺寸及哈希，不重复包含图片 Base64。服务在返回前核对完整 PNG、尺寸、哈希及请求预算。失败与离线预演保持普通 envelope。观察凭证、候选坐标及嵌套授权见[视觉操作流程](DEVELOPER_VISUAL.md)。

## VM 与嵌套动作授权

[访问策略](MCP_ACCESS.md) 继续按 VM UUID 和工具名授权。开启开发模式不扩大策略中的 grant，例如：

```json
{
  "vms": {
    "11111111-1111-1111-1111-111111111111": [
      "vm_info", "doctor_vm", "vm_snapshot_list", "dev.system.info"
    ]
  }
}
```

复合动作需要同时获得外层工具及每个内层动作的 grant。例如工作流启动 VM、运行任务并等待状态，需要该 VM 的 `dev.workflow.run`、`vm_start`、`dev.task.start`、`dev.wait.until` 和 `dev.task.status`。显式授予一个工作流工具不等于授予任意内层动作。工具只有在至少一个 VM 获授权时才列出。

单独执行 `dev.workflow.cleanup` 时只检查实际清理动作的 grant 和路径，仍校验整个 profile 的结构及目标 VM；不会要求未执行步骤的权限。

工作流 profile 的 VM 必须与调用 UUID 相同；不能覆盖服务配置的 `developer_script`。MCP 中不开放克隆、创建或恢复快照、删除 VM，以及恢复/删除式回收；这些操作继续使用 CLI。工作流的停止式回收映射到 `vm_stop`，仍需 VM 写开关和 grant。

## 宿主工作目录

`--developer-workspace` 必须是已存在的目录，目录本身不能是符号链接。未配置时，工具列表不包含上传、下载、同步、安装、证据采集和工作流等需要访问宿主文件的工具。上传、下载、同步、安装来源、证据输出和工作流 profile/输出等宿主路径必须是此目录内的干净绝对路径，不允许 `..` 或目录内的符号链接；新输出可以尚不存在。Windows guest 的 `path`、`target_dir`、任务参数和查询谓词路径不按宿主路径处理。

同步 profile 的 `source` 相对 profile 所在目录解析，来源目录的所有条目也接受检查。工作流在执行前递归检查各步骤、证据观察和同步 profile。宿主路径及整组参数不能来自 `$ref`；任务 ID 等 guest 参数仍可引用先前结果。未知本地动作、动态动作名、重复 JSON 键或无法静态核对的路径会被拒绝，拒绝时不会调用 guest 后端。

宿主文件动作在同一 MCP 实例中串行执行，锁覆盖预检查和完整调用，避免另一个 MCP 文件写操作替换刚检查的配置。普通查询仍共享最多 4 个后端调用的额度；同一 VM 的开发写操作与 VM 操作使用同一写锁，保守覆盖该 VM 的所有 guest 会话。见[并发与取消](MCP_CONCURRENCY.md)。

工作目录是这个 MCP 实例的路径限制，不是 macOS 进程沙箱；拥有宿主文件权限的其他进程仍能更改目录。配置和运行期间应由同一项目维护这些文件，真实桌面与 VM 行为仍需单独验收。
