# 开发动作入口与协议

`dev` 把应用调试拆成可组合的任务：准备文件与环境、启动具名程序、观察窗口、执行输入、验证结果、保存证据。现有 `guest` 只读查询继续使用 `desktop-bridge.ps1`；开发动作使用独立的 `developer-bridge.ps1`。

## 开始使用

先将发行包中的 `guest/windows/developer-bridge.ps1` 和完整 `guest/windows/developer/` 目录一起安装到 Windows 的 `C:\ProgramData\Starbridge\`，保留 modules、workers 和 `.cs` 文件。需要首次安装或整套更新时，见[文件传输与安装](DEVELOPER_FILES.md#安装更新与回退)。Parallels Tools、当前 Windows 用户及相应目录权限仍须可用。

```sh
starbridge dev system.info VM
starbridge --dry-run dev system.info VM
starbridge --developer-script 'D:\Starbridge\developer-bridge.ps1' dev system.info VM
starbridge dev ACTION VM --request parameters.json --allow-write
```

`parameters.json` 直接包含参数对象，如 `{}`，不加 `action` 或 `params` 外层；省略 `--request` 相当于 `{}`。全局参数放在 `dev` 前。写动作必须显式加 `--allow-write`，包括下载文件、工作流和证据导出等会写宿主文件的动作；Go API 与 Windows 入口也执行各自的写授权检查。

`system.info` 返回 Windows、PowerShell/.NET 版本、架构、会话、guest 已注册动作及 helper 指纹，默认不返回用户名或目录。指纹覆盖入口与 `developer/` 下全部 `.ps1`、`.cs`，不是代码签名。动作出现在 capabilities 中表示已安装实现，具体调用仍可能因会话、权限或应用状态失败。MCP 工具列表还取决于宿主编译版本、服务开关和访问策略。

按任务选择后续接口：

- 准备与运行项目：[文件、同步和安装](DEVELOPER_FILES.md) → [具名任务与日志](DEVELOPER_TASKS.md) → [等待与断言](DEVELOPER_WAIT.md)。
- 调试界面：[UIA 搜索](DEVELOPER_UI_QUERY.md) → [UIA 操作、窗口和 Unicode 输入](DEVELOPER_UI_ACTIONS.md) → [结果验证](DEVELOPER_WAIT.md)。
- UIA 不足时：[截图与捕获能力](DEVELOPER_SCREENSHOTS.md) → [视觉候选点击与再次观察](DEVELOPER_VISUAL.md)。
- 重复执行与留存：[项目工作流](DEVELOPER_WORKFLOW.md)、[证据导出](DEVELOPER_EVIDENCE.md)。

从 ChatGPT/Codex 等客户端调用时显式开启 `serve --developer-tools`，配置见[MCP 开发工具](MCP_DEVELOPER.md)。桥接层不接入模型 API 或保存模型凭据。

## 参数校验与预演

CLI 请求文件须为单个 UTF-8 JSON 对象，最多 16000 字节。CLI 与 MCP 拒绝无效 UTF-8、孤立 UTF-16 代理转义；合法的中文、emoji 和代理对可正常传输。动作校验器拒绝未知参数和错误类型，并补齐默认值；最终传输 envelope 连同默认值也必须在 16000 字节内。

`--dry-run` 执行动作参数校验与写授权检查，不访问 VM。`wait.until` / `assert.matches` 还会提前校验选定观察动作的参数。本地主机复合动作在预演中不执行 handler，因此不会打开同步/工作流 profile、helper 源目录或创建输出；预演成功不证明这些文件有效，也不证明 guest 配置、桌面或应用可用。

脱敏按动作实现：任务预演隐藏附加参数与环境值，文本输入隐藏文本，文件和工作流动作隐藏相应路径。其他查询条件、名称或自定义参数仍可能出现在预演中；不要把预演结果当成自动去敏的报告。

## 请求与响应

宿主通过参数向量调用当前 Windows 用户的 PowerShell。直接 guest 动作的传输请求经 Base64 传入 `-RequestBase64`，格式如下；这是内部协议，与 CLI 请求文件不同：

```json
{"action":"system.info","params":{}}
```

入口只加载相邻的 `developer/*.ps1`，workers 由模块显式调用。安装的模块是可信可执行代码，请求不能指定任意模块或脚本路径。

响应使用 `ok:true,data:{...}` 或 `ok:false,error:{code,message,...}`。直接 Windows 动作还带 `meta`：`protocol_version:"dev-1"`、`action`、`read_only`、`session_id`、`user_interactive`、`timestamp`、`duration_ms`；stdout 为一行 JSON，上限 8 MiB，成功/失败退出码分别为 0/1。宿主核对协议、动作、会话、读写属性、结果及请求预算；交互动作成功必须来自非零交互会话。本地主机复合动作和宿主传输错误不承诺带 Windows metadata。

超时、取消或输出超限不能证明 guest 已停止，写操作也不会自动撤销。写传输出现这些错误时，`error.details.outcome:"unknown"` 表示必须检查实际状态；部分 UI 动作能明确报告 `not_started`。`ui.click` 或 `input.text` 已提交后，响应验证失败同样不能当作未执行。查看具体动作的结果语义，不要用 `ok:true` 或 `dispatched` 代替业务后置条件。

## 扩展模块

Go 在 `init` 中调用 `registerDeveloperAction(DeveloperAction{...})`，提供名称、描述、读写/交互属性、`Normalize` 和 `ValidateResult`。`ValidateWithinRequest` 检查结果与本次身份、预算一致；`RedactPreview` 处理预演敏感参数。`DeveloperActions()` 返回排序副本，注册表初始化后只读。

本地主机动作可提供 `Execute` 替代 Windows 传输，仍先经过参数校验、写授权和预演处理。MCP 对本地主机动作另有显式允许列表和路径策略；仅注册动作不会自动开放宿主文件访问。

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

`Get-DevString $p name default max [-Required]` 按 Unicode 码点计长；缺失使用默认值，显式 null、错误类型、NUL 或超长会拒绝。整数要求精确且有界，布尔值不接受字符串或数字。`Assert-DevFields` 拒绝未知字段，`Get-DevField` 保留数组。handler 只返回一个数据对象；用 `Stop-Dev CODE MESSAGE [DETAILS]` 返回稳定错误，不把包含私密路径或输入的异常消息直接回传。

源码中的合成测试可在 Windows PowerShell 5.1 运行：

```powershell
& .\tests\windows\developer-contract.ps1
```

该测试不依赖真实桌面；模块专项测试及[真实桌面验收](DEVELOPER_ACCEPTANCE.md)是独立层次。
