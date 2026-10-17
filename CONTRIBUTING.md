# 参与贡献

欢迎改进 Parallels 后端、Windows 桌面查询、文档和测试。

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

Windows helper 在 Windows PowerShell 5.1 中检查：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\tests\windows\guest-smoke.ps1
```

真实桌面与 VM 操作另按[验收指南](docs/ACCEPTANCE.md)验证；安装包检查见[发布说明](docs/RELEASING.md)。

## 修改与验证

- 保持 JSON 输出、退出码与错误契约清楚、可预测；标准输出不能夹入诊断日志。
- 宿主命令使用参数数组；guest 查询只接受明确动作和经过验证的参数。
- 副作用操作保留预演、目标确认与超时处理；测试使用专用 VM，删除测试只使用可丢弃的克隆。
- 修改协议或行为时同步更新相关示例与文档，并提供验证该行为的必要测试。文档修订无需新增复述实现的测试。
- 在贡献说明中写明修改目的、已执行检查及尚未验证的环境。替身测试通过不能替代真实 Parallels 或 Windows UIA 验收。
- 提供错误样例或界面输出时，去除真实 VM 标识、用户路径、窗口标题及其他与问题无关的个人信息。

提交贡献前请确认有权按本仓库的 [GPLv3 许可](LICENSE)提供相应内容。
