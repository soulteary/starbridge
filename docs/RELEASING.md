# 发布配套安装包

本页描述源码打包与发布流程，不表示仓库已经提供可下载的 Release 资产。没有与目标版本对应的资产时，从源码构建；以后下载资产时，也应核对版本、配套 helper 与校验和。

在源码检出目录执行 `scripts/package-release.sh VERSION`，构建 darwin arm64/amd64 两种包。版本必须符合 [SemVer 2.0.0](https://semver.org/spec/v2.0.0.html)，可带一个 `v` 前缀；支持合法的预发布标识与构建元数据。主/次/修订版本及纯数字预发布标识不允许前导零，构建元数据允许。

发布使用 `.go-version` 固定的工具链，普通 CI 使用 [go.mod](../go.mod) 的最低兼容版本。本地需选择匹配的已安装工具链；脚本会核对工具链、第三方声明及构建后二进制的 GoVersion，版本不一致时拒绝归档。升级工具链时同步核对 Go 许可与专利授权内容。

每个包包含宿主二进制、配套 Windows helper、文档、示例、Windows 验收脚本与夹具、许可、go.mod 和 BUILD.txt。开发 helper 必须包含完整 `developer/` 与嵌套 workers，包括任务和 WGC 的 C# 源码；Windows PowerShell 5.1 在运行时使用现有 .NET Framework 编译，无需用户安装 SDK、下载 NuGet 或另装编译器。BUILD.txt 记录版本、commit、dirty、平台、Go 与两套 guest 协议版本；`checksums.txt` 用于校验归档。

静态文件取自规定范围内的 Git 跟踪路径，保留工作区修改并记录 dirty；ignored / untracked 文件不入包。缺失文件、符号链接或非普通文件会使打包失败。归档前检查随包 Markdown 的相对文件链接。包内不含 Go 源码、Git 数据或 CI 配置，文档中的构建与维护命令须在源码目录执行。

```sh
./scripts/package-release.sh 0.1.0
cd dist
shasum -a 256 -c checksums.txt
```

可用 `STARBRIDGE_DIST_DIR` 指定输出目录。脚本只清理自己创建的临时目录；打包不会启动 VM、安装 helper 或发布 Release。

普通 CI 和发布前检查会运行打包回归测试。测试使用构建与发布替身，验证版本规则、双架构归档、校验和、文件链接及发布参数，不编译二进制、联网或发布版本：

```sh
./tests/release/package-release-test.sh
```

实际构建后可解包，并使用 `./scripts/check-package-docs.sh EXTRACTED_PACKAGE_DIRECTORY` 再次检查相对链接目标。

## 发布前检查与自动门槛

维护者发布前应核对目标提交的[普通 Tests 工作流](https://github.com/soulteary/starbridge/actions/workflows/test.yml)，其中包含全部 Windows developer 合成 contracts。真实 Parallels、桌面输入、视觉点击和 WGC 窗体捕获按[验收指南](ACCEPTANCE.md)分别记录；未执行的环境保持待验收，不用合成结果或 opt-in 跳过记录代替。

推送 `v` 前缀版本标签会触发 [Release 工作流](https://github.com/soulteary/starbridge/actions/workflows/release.yml)。其当前自动门槛为：

1. macOS 使用 `.go-version` 工具链执行打包契约、Go 竞态与静态检查。
2. Windows PowerShell 执行 `guest-smoke.ps1`，其中包含共享参数契约。这个发布 job 没有执行普通 Tests 中的全部 developer contracts，也没有运行真实桌面验收。
3. 上述 job 成功后，构建两种架构的包，核对二进制工具链、第三方声明、文档相对链接，并发布归档与校验和。标签版本仍需通过打包脚本的 SemVer 校验。

带预发布标识的标签（如 `v1.2.3-rc.1`）会创建 GitHub prerelease。仅构建元数据含连字符（如 `v1.2.3+build-alpha`）仍按稳定版发布。

## 发布资料与签名

发布前审阅文档、示例、脚本和测试资料，避免归档真实 VM 标识、个人目录、访问凭据或桌面响应。已跟踪的支持文件会入包；自动隐私检查的范围见[测试资料要求](DEVELOPER_ACCEPTANCE.md#隐私检查与结果记录)。

当前工作流不验证 Git 提交或标签的密码学签名；`gh release create --verify-tag` 只确认远端标签存在。`checksums.txt` 用于校验归档，安装包尚未执行 Apple Developer ID 签名或公证。
