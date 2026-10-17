# 参与贡献

欢迎改进 Parallels 后端、Windows 开发工具、文档和测试。

## 本地开发

安装满足 [go.mod](go.mod) 要求的 Go 工具链，从项目根目录执行：

```sh
go fmt ./...
go test -race ./...
go vet ./...
go build -o bin/starbridge ./cmd/starbridge
```

命令预演不要求运行 Parallels：

```sh
go run ./cmd/starbridge --dry-run vm start "Windows 11"
```

Windows helper 使用 Windows PowerShell 5.1。先运行基础 smoke，再按[完整合成检查清单](docs/ACCEPTANCE.md#自动化检查范围)运行 developer contracts：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\guest-smoke.ps1
```

合成检查包含实际 C# 编译、临时任务进程、文件传输，以及 WGC 的无窗口 D3D11/WARP 资源往返。UIA、文字输入与视觉流程的替身检查不能证明真实应用支持这些操作。真实桌面与 VM 操作另按[验收指南](docs/ACCEPTANCE.md)验证；WGC 窗口捕获需要独立显式启用，安装包检查见[发布说明](docs/RELEASING.md)。

## 修改与验证

- 保持 JSON 输出、退出码与错误契约清楚、可预测；标准输出不能夹入诊断日志。
- 宿主命令使用参数数组；guest 只接受已注册动作和经过验证的参数。保持旧查询与开发动作的授权边界，VM 写授权与 guest 写授权不能互相代替。
- 副作用操作保留预演、目标确认、超时与未知结果处理；不要自动重试输入或其他写操作。真实测试使用专用 VM，删除测试只使用可丢弃的克隆。
- 修改协议或行为时同步更新相关示例与文档，并提供验证该行为的必要测试。文档修订无需新增复述实现的测试。
- 在贡献说明中写明修改目的、已执行检查及尚未验证的环境。替身测试通过不能替代真实 Parallels 或 Windows UIA 验收。
- 提供错误样例或界面输出时，去除真实 VM 标识、用户路径、窗口标题及其他与问题无关的个人信息。

测试资料只使用合成数据、运行时生成的标识和示例域名。`internal/privacy` 会检查暂存区与工作区中的测试资料，包括未忽略的新文件；它不能替代对日志、截图和修改内容的人工检查。检查范围与专用 runner 配置见[开发验收](docs/DEVELOPER_ACCEPTANCE.md)。

提交贡献前请确认有权按本仓库的 [GPLv3 许可](LICENSE)提供相应内容。
