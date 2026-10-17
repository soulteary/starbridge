# 开发工作流验收与测试资料

普通测试验证主机逻辑、协议和 Windows 合成契约。真实 Parallels 传输与桌面操作使用独立的选择性验收，不因测试编译通过、fake 返回成功或工作流文件存在而视为已完成。

## 默认检查

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o bin/starbridge ./cmd/starbridge
./tests/release/package-release-test.sh
```

`tests/integration` 使用 `desktopintegration` build tag，默认不参与 `go test ./...`。即使指定该 tag，缺少 `STARBRIDGE_RUN_DESKTOP_ACCEPTANCE=1` 时，桌面测试也会跳过；此跳过行为可以离线验证：

```sh
go test -tags=desktopintegration ./tests/integration -run TestDeveloperDesktopWorkflow -count=1 -v
```

该命令的 `SKIP` 只证明没有启用桌面操作。普通 Windows CI 解析合成窗体脚本，不会因此启动此窗体。现有 Windows 本地桌面验收见[桌面验收指南](DESKTOP_TESTING.md)。

## 准备独立夹具环境

使用可丢弃的专用 Windows VM、专用 Windows 测试账户和专用 macOS runner。VM 中只保留测试应用与合成数据，登录解锁的交互桌面；验收期间不使用其他应用，也不让其他任务控制同一桌面。安装 Parallels Tools 和配套的完整 developer helper。

此工作流依赖 `task.start/status/output/cancel/close`、`ui.query`、`ui.act`、`wait.until` 与 `screenshot.capture`。运行前确认主机二进制与 guest helper 都包含这些能力；缺少能力会使验收失败，不会自动安装、降级或改用其他应用。

将 [`developer-fixture.ps1`](../tests/windows/developer-fixture.ps1) 单独复制到测试账户可访问的固定目录。为该账户配置唯一的 `starbridge-fixture` task profile，执行 Windows PowerShell 5.1，参数固定为 `-NoLogo -NoProfile -Sta -ExecutionPolicy Bypass -File` 和夹具脚本的绝对路径：

```json
{
  "version": 1,
  "profiles": {
    "starbridge-fixture": {
      "executable": "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
      "cwd": "C:\\StarbridgeFixture",
      "args": ["-NoLogo", "-NoProfile", "-Sta", "-ExecutionPolicy", "Bypass", "-File", "C:\\StarbridgeFixture\\developer-fixture.ps1"],
      "env": {},
      "allowed_env": [],
      "allow_extra_args": false,
      "interactive": true,
      "show_window": true,
      "timeout_ms": 120000,
      "max_output_bytes": 1048576
    }
  }
}
```

路径是合成示例，按专用 VM 的实际安装位置配置。配置文件放在测试账户的 `%LOCALAPPDATA%\Starbridge\Developer\profiles.json`；隔离配置也可使用该账户的 `STARBRIDGE_DEV_HOME`。不要把此 profile 指向用户应用，不允许额外参数或环境覆盖。测试不会在请求中提供任意命令。

夹具只显示计数器、中文按钮和空文本框，向自身 stdout 写入本次进程的 PID、窗口 HWND 和启动时间；不枚举其他应用，不读取用户文件。

## 选择性运行

GitHub 的 [`Dedicated desktop integration`](https://github.com/soulteary/starbridge/blob/main/.github/workflows/desktop-integration.yml) 只支持 `workflow_dispatch`，需要显式勾选 `fixture_environment_confirmed`，并使用带有 `self-hosted`、`macOS`、`starbridge-desktop` 标签的专用 runner。普通 push、PR 和共享 hosted runner 不会触发它。

为该仓库或受控环境配置：

- secret `STARBRIDGE_TEST_VM_UUID`：专用夹具 VM 的完整 UUID，不写入测试源码、请求样例或报告。
- 可选 variable `STARBRIDGE_TEST_DEVELOPER_SCRIPT`：该 VM 中 developer helper 的绝对路径；未设置时使用默认安装位置。

工作流为测试显式设置 `STARBRIDGE_RUN_DESKTOP_ACCEPTANCE=1`。仅在已配置的隔离环境中，才使用同样的本地环境变量运行带 tag 的测试；不要将 UUID 或本机路径直接写入可提交的脚本。

测试从固定 profile 启动自己的任务，从其有界 stdout 获取窗口身份，再与 `task.status` 的 PID 和进程启动时间核对。随后定位已知中文按钮，执行一次 UIA Invoke，等待计数器变为 `1`，检查该窗口截图的 PNG 编码与非空尺寸，正常关闭任务并等待退出。测试退出时尝试取消本次创建的任务；profile 的两分钟超时为任务提供额外边界。

测试不会自动开启、停止、恢复或删除 VM，不会重试 UI 写操作。截图只在内存中检查，工作流不上传截图、原始响应或任务日志。如果出现超时或连接中断，仍须在专用环境中检查本次任务是否已经退出。

## 隐私检查与结果记录

`internal/privacy` 检查 `tests/`、`examples/` 与 `internal/**/*_test.go` 中的 Git 暂存内容，以及工作区中已跟踪和未忽略的新测试资料。它也检查自己的源码，并用临时合成仓库验证：工作区已清除的敏感模式仍留在 index 时，检查会失败。

检查拒绝个人主目录路径、常见密钥/token 和非示例邮箱模式；发现内容时只报告仓库内文件名，隐藏命中的值。它是针对这些模式的回归检查，不替代对测试资料和截图内容的人工审阅。只使用合成账号、`example.test` 等示例域名和运行时生成的标识；不要把真实 VM UUID、访问凭据、个人目录或桌面响应写进 fixture。

集成测试失败信息仅包含动作名、稳定错误代码或断言描述，不回显原始 guest 响应、stdout/stderr、UUID、环境值或运行时路径。需要调查时，在隔离环境中单独查看具体证据，公开记录前先检查内容。

验收结果记录提交或发布版本、Parallels/Tools 与 Windows 版本、架构、交互会话条件、执行场景和实际通过/失败结果。没有执行过的场景保留为待验收；默认 CI 的成功或 opt-in 测试的跳过不能替代真实桌面执行结果。通用 VM 验收步骤见[验证与验收](ACCEPTANCE.md)。
