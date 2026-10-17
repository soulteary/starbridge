# 发布配套安装包

`scripts/package-release.sh VERSION` 构建 darwin arm64/amd64 两种包。每个包包含宿主二进制、同一源码快照的 Windows helper、安装说明、使用文档、许可与 BUILD.txt（版本、commit、dirty、平台和 guest 协议版本）。`checksums.txt` 使用不含本机绝对路径的文件名。

```sh
./scripts/package-release.sh 0.1.0
cd dist
shasum -a 256 -c checksums.txt
```

本地可用 `STARBRIDGE_DIST_DIR` 指定输出目录。脚本会清理自己创建的临时目录，不删除已有发布目录；打包不启动 VM、不安装 helper、不发布 Release。开发快照会如实记录 dirty 状态。

维护者推送 `vMAJOR.MINOR.PATCH` 标签时，Release 工作流先运行 macOS Go 竞态/静态检查和 Windows helper smoke，两者成功后才构建并发布资产。只提交或合并本 PR 不会发布版本。本次修改没有推送标签或发布 Release。

发布前仍需按 ACCEPTANCE.md 完成真实桌面与 Parallels 验收。当前安装包未做 Apple Developer ID 签名与公证；不要通过脚本自动移除系统隔离属性。
