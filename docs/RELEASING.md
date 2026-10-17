# 发布配套安装包

在源码检出目录执行 `scripts/package-release.sh VERSION`，构建 darwin arm64/amd64 两种包。版本必须符合 [SemVer 2.0.0](https://semver.org/spec/v2.0.0.html)，可带一个 `v` 前缀；支持合法的预发布标识与构建元数据。主/次/修订版本及纯数字预发布标识不允许前导零，构建元数据允许。

发布使用 `.go-version` 固定的工具链，普通 CI 使用 [go.mod](../go.mod) 的最低兼容版本。本地需选择匹配的已安装工具链；脚本会核对工具链、第三方声明及构建后二进制的 GoVersion，版本不一致时拒绝归档。升级工具链时同步核对 Go 许可与专利授权内容。

每个包包含宿主二进制、配套 Windows helper、文档、示例、Windows 验收脚本与夹具、许可、go.mod 和 BUILD.txt。BUILD.txt 记录版本、commit、dirty、平台、Go 与 guest 协议版本；`checksums.txt` 用于校验归档。

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

推送合法的 `v` 前缀版本标签时，[Release 工作流](https://github.com/soulteary/starbridge/actions/workflows/release.yml)先运行 macOS Go 竞态/静态检查和 Windows helper smoke，通过后构建并发布资产。

带预发布标识的标签（如 `v1.2.3-rc.1`）会创建 GitHub prerelease。仅构建元数据含连字符（如 `v1.2.3+build-alpha`）仍按稳定版发布。

发布前按[验收指南](ACCEPTANCE.md)完成真实桌面与 Parallels 检查。当前安装包未做 Apple Developer ID 签名与公证。
