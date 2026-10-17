# 开发动作协议

`dev` 为开发任务提供独立入口；现有 `guest` 只读查询继续使用原 helper。
先将 `guest/windows/developer-bridge.ps1` 和整个 `guest/windows/developer/` 目录一起安装到 Windows 的 `C:\ProgramData\Starbridge\`。目录中的 workers 也需要保留。

```sh
./bin/starbridge dev system.info VM
./bin/starbridge --dry-run dev system.info VM
./bin/starbridge --developer-script 'D:\Starbridge\developer-bridge.ps1' dev system.info VM
./bin/starbridge dev ACTION VM --request parameters.json --allow-write
```

`parameters.json` 直接包含参数对象，例如 `{}`，不包含 `action` 或 `params` 外层。省略 `--request` 相当于 `{}`。未知动作、参数或不符合类型的值会被拒绝；写动作必须显式加 `--allow-write`，Go API 和 Windows 入口也执行此检查。预演完成参数校验但不访问 VM；注册了脱敏函数的动作会在预演中隐藏敏感参数。

`system.info` 返回 Windows 版本、系统与进程架构、PowerShell/.NET 版本、会话、已安装动作以及 helper 代码指纹。默认不返回用户名或目录。`capabilities` 表示安装了对应实现；具体动作仍可能因会话、权限、应用状态或配置而不可用。代码指纹覆盖入口和 `developer/` 下所有 `.ps1`、`.cs` 文件，包括 workers；它不是代码签名。

通过 MCP 调用时显式开启 `serve --developer-tools`；独立写授权、动作策略及宿主工作目录见[MCP 开发工具](MCP_DEVELOPER.md)。

## 请求与响应

主机使用参数向量调用当前 Windows 用户的 PowerShell，不拼接请求内容为命令。请求为最多 16000 字节的 UTF-8 JSON，经 Base64 传入 `-RequestBase64`：

```json
{"action":"system.info","params":{}}
```

入口只加载紧邻的 `developer/*.ps1`；嵌套 workers 由各模块显式调用。模块属于本地安装的可信代码，不能从请求指定模块或脚本路径。修改 helper 树等同于修改可执行程序。

响应为 stdout 上的一行 JSON，最多 8 MiB。成功包含 `ok: true`、对象 `data` 和 `meta`，退出码为 0；失败包含 `ok: false`、`error.code`、`error.message` 和 `meta`，退出码为 1。metadata 包含 `protocol_version: "dev-1"`、`action`、`read_only`、`session_id`、`user_interactive`、`timestamp` 和 `duration_ms`。主机核对协议、动作、读写属性、会话类型以及动作结果结构；交互动作的成功响应必须来自非零交互会话。

超时、取消或输出超限只约束主机等待，不能保证 guest 已停止或写操作已撤销。写操作返回这些传输错误时，`error.details.outcome` 为 `unknown`，应查询实际状态后再决定是否重试。动作自身的 guest 错误需要按对应模块语义处理。

## 扩展模块

Go 模块在 `init` 中调用 `registerDeveloperAction(DeveloperAction{...})`，提供名字、描述、读写/交互属性、`Normalize` 和 `ValidateResult`。`ValidateWithinRequest` 可进一步核对结果没有超过本次请求预算；`RedactPreview` 隐藏预演参数。`DeveloperActions()` 返回按名称排序的注册项副本。注册表初始化后只读，不支持运行时插件加载。

本地主机复合动作可以提供 `Execute` 替代 Windows 传输；同样先执行通用校验、写授权和离线预演。此 handler 负责自己的结果结构与 metadata，不应伪造 Windows 会话。

```powershell
Register-DeveloperAction -Name 'example.inspect' -Handler {
    param($p)
    Assert-DevFields $p @('label', 'limit', 'enabled')
    return [ordered]@{
        label = (Get-DevString $p 'label' 'default' 256)
        limit = (Get-DevInteger $p 'limit' 10 1 100)
        enabled = (Get-DevBoolean $p 'enabled' $false)
    }
} -Mutation:$false -Interactive:$false -Description 'Inspect an example.'
```

`Get-DevString $p name default max [-Required]` 按 Unicode 码点计长；缺失使用默认值，显式 null、错误类型或超长会拒绝。`-Required` 要求存在且非空。整数必须精确、有限并在范围内，布尔值不接受字符串或数字。`Assert-DevFields` 拒绝未知字段；`Get-DevField` 保留数组类型。模块返回一个对象，不自行输出 envelope；用 `Stop-Dev CODE MESSAGE` 返回稳定错误，避免把异常中的私密路径和环境值带给调用方。

在项目根目录的 PowerShell 会话中运行基础契约测试：`& .\tests\windows\developer-contract.ps1`。它验证真实 PowerShell 子进程、语法、结构、默认值、数组、Unicode、代码指纹与写授权，使用合成模块，不依赖交互桌面；它不替代真实 VM 与桌面验收。

从 UIA 或图片候选定位后操作，可使用[观察、视觉点击与再次观察](DEVELOPER_VISUAL.md)；它保留窗口身份、坐标映射和待验证状态。
