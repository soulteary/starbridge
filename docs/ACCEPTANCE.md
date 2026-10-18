# 验证与验收

自动化检查验证代码和协议契约；桌面访问与虚拟机操作还需在目标环境验收。具体提交的检查结果见 [Tests 运行记录](https://github.com/soulteary/starbridge/actions/workflows/test.yml)，发布门槛见 [Release 工作流](https://github.com/soulteary/starbridge/blob/main/.github/workflows/release.yml)。

## 自动化检查范围

[测试工作流](https://github.com/soulteary/starbridge/blob/main/.github/workflows/test.yml) 包含：

- Linux 与 macOS：Go 测试、竞态检查、静态检查、二进制构建，以及不编译或发布安装包的打包契约测试。宿主测试通过替身验证 Parallels 命令、guest 响应与 MCP 协议，不操作真实 VM。
- Windows PowerShell 5.1：基础 helper smoke、共享参数契约、全部已安装 developer 合成 contracts，并解析 `tests/windows/*.ps1`。smoke 可接受空窗口列表或明确的非交互会话错误；真实 UIA 正向查询另按桌面验收执行。

当前 Windows developer 合成检查包括：

- `developer-contract.ps1`：动作注册、协议、写授权、输入与配套文件身份。
- `developer-tasks.ps1`：临时编译的任务程序、参数与环境、退出码、有界输出、PID 身份、取消进程树、超时、命令行边界和启动失败清理；可用交互会话还检查自建 GUI 的正常关闭。
- `developer-files.ps1`、`developer-bootstrap.ps1`：临时目录内的分块读写、哈希、冲突、路径限制、配套目录安装、更新及回退。
- `developer-ui-query-contract.ps1`、`developer-ui-action-contract.ps1`：合成 UIA 控件、遍历预算、密码值限制、worker 超时、会话锁与动作分派；不向已有窗口发送输入。
- `developer-input-text-contract.ps1`：实际编译 Unicode 输入适配器，验证中文、emoji、UTF-16 事件、换行原样保留、长度边界及部分分派清理；插入委托为替身，不调用真实 `SendInput`。
- `developer-screenshot-contract.ps1`、`developer-wgc-contract.ps1`：实际编译截图 C#，验证生成的 PNG、字节/像素预算、错误码、边距裁剪和缓冲区；WGC 探测 OS/API/设备，并以 WARP 对匿名像素运行真实 D3D11 资源创建、复制和读回，不捕获窗口。
- `developer-visual-contract.ps1`：生成图片、合成观察、DPAPI 观察令牌、坐标映射、过期与身份校验、点击前后结果及 UIA 降级；窗口、截图和输入分派使用替身。

这些检查会创建自己的临时文件、进程和同步对象。它们不连接 Parallels，不操作现有用户应用；合成成功、API 探测成功和脚本语法通过都不能替代真实窗口的输入或截图验收。具体脚本以[工作流的匹配规则](https://github.com/soulteary/starbridge/blob/main/.github/workflows/test.yml)为准。

在源码目录复现宿主检查：

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o bin/starbridge ./cmd/starbridge
./tests/release/package-release-test.sh
```

在 Windows 项目目录运行基础检查：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\guest-smoke.ps1
```

随后在 Windows PowerShell 中执行与普通 CI 相同的 developer 选择规则，每个脚本使用独立进程，避免已加载的 C# 类型或测试替身互相影响：

```powershell
$contracts = Get-ChildItem tests/windows/developer-*.ps1 -File |
  Where-Object { $_.Name -match '^developer-(contract|tasks|files|bootstrap|.+-contract)\.ps1$' } |
  Sort-Object Name
foreach ($contract in $contracts) {
  $command = "& '" + $contract.FullName.Replace("'", "''") + "'"
  $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
  & powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand $encoded
  if ($LASTEXITCODE -ne 0) { throw ('Synthetic contract failed: ' + $contract.Name) }
}
```

## 桌面端到端验收

按[桌面验收指南](DESKTOP_TESTING.md)先验证已知窗口与中文按钮，再经 Parallels 对照同一窗口的 PID、HWND、会话与坐标。该指南另列 Unicode 输入、视觉点击与 WGC 的真实验收范围。可选的专用 VM 自动流程见[开发验收](DEVELOPER_ACCEPTANCE.md)；它没有覆盖的场景仍需单独执行并记录。

## 虚拟机操作验收

使用专用测试 VM 或可丢弃的克隆，先通过 `vm list` 确认 UUID。命令与回收策略格式见[安装与使用](USAGE.md)。

1. 每种操作先用 `--dry-run` 核对目标和参数，再验证启动、正常关机、挂起、恢复与克隆。通过 `vm info` 和 guest 查询检查操作后的实际状态与就绪情况。
2. 在 guest 本地磁盘创建标记文件并保存快照，修改文件后恢复快照，确认文件和 VM 状态符合预期。恢复、删除及强制断电按命令要求提供确认标识。
3. 分别验证 `recycle` 的 `stop`、`restore`、`delete` 模式，以及默认策略、VM 专属策略和命令行覆盖。删除场景只使用可丢弃的克隆；确认策略失败后没有自动改用其他操作。
4. 记录超时或连接中断后的实际 VM 状态，再决定是否重试。命令返回成功后仍需检查 guest 是否已完成启动或关机。

## 保存验收结果

每次验收记录提交或发布版本、Windows 与 Parallels / Tools 版本、CPU 架构、登录方式、测试场景、实际结果和相关 JSON 输出。桌面场景补充显示器排列与缩放比例。按自动化检查、Windows 内验收、Parallels 传输和 VM 操作分别记录结果，便于复验。
