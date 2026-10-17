# 发布配套安装包

在源码检出目录执行 `scripts/package-release.sh VERSION`，构建 darwin arm64/amd64 两种包。版本必须符合 [SemVer 2.0.0](https://semver.org/spec/v2.0.0.html)，可带一个 `v` 前缀；支持合法的预发布标识与构建元数据。主/次/修订版本及纯数字预发布标识不允许前导零，构建元数据允许。

每个包包含宿主二进制、同一源码快照的 Windows helper、完整 docs、README、贡献说明、go.mod、示例、文档引用的 Windows 验收脚本与夹具、许可和 BUILD.txt（版本、commit、dirty、平台和 guest 协议版本）。包内不包含 Go 源码、Git 数据或 CI 配置；README、CONTRIBUTING 和本页的构建及维护命令面向源码检出目录。`checksums.txt` 使用不含本机绝对路径的文件名。归档前会检查所有随包 Markdown 内联相对链接的文件目标，避免遗漏文档或示例。

```sh
./scripts/package-release.sh 0.1.0
cd dist
shasum -a 256 -c checksums.txt
```

本地可用 `STARBRIDGE_DIST_DIR` 指定输出目录。脚本会清理自己创建的临时目录，不删除已有发布目录；打包不启动 VM、不安装 helper、不发布 Release。开发快照会如实记录 dirty 状态。

普通 CI 和发布前检查会运行以下快速打包回归测试。测试用构建替身验证版本规则、双架构归档、校验和、配套 helper 及文档链接，并用 gh 替身检查工作流发布命令的参数，不编译二进制、联网或发布版本：

```sh
./tests/release/package-release-test.sh
```

实际构建后可解包，并使用 `./scripts/check-package-docs.sh EXTRACTED_PACKAGE_DIRECTORY` 再次检查相对链接目标。

维护者推送 `vMAJOR.MINOR.PATCH` 标签时，Release 工作流先运行 macOS Go 竞态/静态检查和 Windows helper smoke，两者成功后才构建并发布资产。只提交或合并本 PR 不会发布版本。本次修改没有推送标签或发布 Release。

带预发布标识的标签（如 `v1.2.3-rc.1`）会创建 GitHub prerelease。仅构建元数据含连字符（如 `v1.2.3+build-alpha`）仍按稳定版发布。

发布前仍需按 ACCEPTANCE.md 完成真实桌面与 Parallels 验收。当前安装包未做 Apple Developer ID 签名与公证；不要通过脚本自动移除系统隔离属性。
