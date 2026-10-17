# 文件传输、增量同步与 helper 安装

所有写入都需要 `--allow-write`，包括写入 macOS 文件的 `files.download`。预演使用全局 `--dry-run`：验证参数并隐藏目录和文件内容，不打开源文件、不连接虚拟机。命令格式：

```sh
starbridge dev files.upload VM --request upload.json --allow-write
starbridge dev files.download VM --request download.json --allow-write
starbridge dev files.sync VM --request sync-request.json --allow-write
```

## 配置 Windows 文件根目录

在 Windows 开发账户的 `%LOCALAPPDATA%\Starbridge\Developer\files.json` 中显式配置允许访问的目录。测试或独立开发环境可设置 `STARBRIDGE_DEV_HOME`，配置文件改为该目录中的 `files.json`。程序不会搜索其他用户目录。

```json
{"version":1,"roots":{"workspace":"C:\\Projects\\Demo","artifacts":"C:\\Projects\\Demo\\out"}}
```

根目录须预先存在。请求使用根目录别名和相对路径，支持 `/` 或 `\`，返回路径统一使用 `/`。拒绝 `..`、绝对路径、NTFS alternate stream、Windows 保留文件名和各级 reparse point。传输暂存目录 `.starbridge-transfers` 不允许作为普通文件路径。

这是当前 Windows 账户下的开发工具；目录配置与 helper 代码必须来自可信来源，不能把同一账户的并发文件系统修改视作隔离边界。

## 上传与产物下载

`upload.json`：

```json
{"local_path":"/absolute/project/build/app.exe","root":"workspace","path":"bin/app.exe","overwrite":false}
```

`download.json`：

```json
{"root":"artifacts","path":"test-results.json","local_path":"/absolute/project/results/test-results.json","overwrite":false}
```

macOS 路径必须显式给出、为规范绝对路径，父目录须已存在，路径中的符号链接会被拒绝。单文件上限为 1 GiB。已有目标默认返回 `FILE_EXISTS`；需要替换时显式设置 `overwrite:true`。

下载发布前再次检查本地目标的身份、大小和修改时间；传输期间目标被修改、替换、创建或删除时返回 `FILE_CHANGED`，保留目标现状和下载临时文件。源文件与配置必须是普通文件，不接受命名管道。同步在任何传输前拒绝会指向同一 Windows 路径的大小写冲突。

上传按 6144 字节分块，检查连续 offset、最终长度和 SHA-256，再在目标文件系统内完成替换。不会先删除旧目标。下载每块上限 65536 字节，核对文件版本及最终 SHA-256，在目标目录内写入 `.starbridge-part-*` 临时文件；校验完成后发布。失败时保留临时文件和已有目标，用户可检查或移除临时文件。

连接超时或中断不能证明 Windows 操作没有发生。写操作不自动重试；检查文件哈希和传输状态后再操作。低层传输返回的 `transfer_id` 可用于主动取消已知暂存上传，程序不会自动取消结果未知的提交。

## 增量同步

`sync-request.json` 只指定配置文件：

```json
{"profile":"/absolute/project/sync.json"}
```

`sync.json`：

```json
{"source":"./build","root":"workspace","path":"bin","exclude":["**/.git/**","**/*.pdb"],"overwrite":false}
```

`source` 相对于配置文件所在目录，不能为绝对路径或包含 `..`；不跟随符号链接。`exclude` 使用 `/` 分段 glob，`*`、`?`、`[]` 匹配单段，`**` 匹配任意层目录。匹配目录时跳过整棵子树。

同步比较 SHA-256，跳过内容相同的文件，仅上传缺失或内容不同的文件。内容不同且已有目标时，仍需 `overwrite:true`。一次最多 2000 个文件、10000 个目录项，总计 4 GiB。不会删除 Windows 端多余文件。返回 `file_count`、`uploaded`、`skipped` 和 `deleted:0`。同步按文件完成，不是目录事务；发生错误后，先前成功的文件仍已更新。

## 安装、更新与回退

安装通过二进制内嵌的固定 loader 执行，不要求 Windows 已有开发 helper。Parallels Tools 和相应 Windows 账户会话仍须可用。

```sh
starbridge dev bootstrap.install VM --request install.json --allow-write
starbridge dev bootstrap.rollback VM --request rollback.json --allow-write
```

`install.json`：

```json
{"source_dir":"/absolute/starbridge/guest/windows","target_dir":"C:\\ProgramData\\Starbridge","overwrite":false}
```

`source_dir` 选择完整配套的 `guest/windows`：必须包含 `desktop-bridge.ps1`、`developer-bridge.ps1`、`developer/system.ps1`，并包括 `developer/` 下所有模块和 worker。只传输 helper 的 `.ps1`、`.cs` 源文件；不会安装用户配置。最多 256 个文件、单文件 8 MiB、合计 64 MiB。`target_dir` 必须显式指定 Windows 驱动器绝对目录，父目录须已存在，目录中各级 reparse point 会被拒绝。

`overwrite:true` 仅允许更新本工具管理的完整安装：旧目录必须包含有效 manifest，文件内容和清单须一致，提交前会再次校验。未知目录返回 `BOOTSTRAP_UNMANAGED_TARGET`；手工安装或包含其他文件的目录请改用新目录安装。

回执存在时必须属于当前安装，提交前检查其身份未变；首次安装不会替换旁边的陌生回执文件。回退同时校验当前安装与备份的完整清单和哈希，损坏备份不会替换有效安装。

上传到目标旁的独立暂存目录后，loader 核验完整 manifest、各文件长度和 SHA-256；更新时将原目录移至旁边的备份，再切换新目录。目录切换期间会有短暂空档，避免同时调用 helper。结果返回 `installation_id`、`manifest_sha256`、`file_count`、`backup_available`。

安装不启动或停止虚拟机，不安装 Parallels Tools，也不运行被安装的脚本。安装到非默认位置后，使用全局 `--guest-script`、`--developer-script` 指向实际脚本。

`rollback.json` 使用上一次成功安装返回的标识：

```json
{"target_dir":"C:\\ProgramData\\Starbridge","installation_id":"0123456789abcdef0123456789abcdef"}
```

有备份时才可回退；当前 helper 内容与 manifest 不一致时拒绝。回退保留被替换的新目录供检查。备份、暂存目录和安装记录位于目标旁边，不会自动清理。首次安装没有旧版本可以回退。

`BOOTSTRAP_RECOVERY_REQUIRED` 表示目录切换后的恢复或安装记录未完成；应检查目标、旁边备份和暂存目录后再操作，不能自动重试覆盖。

## 低层文件动作

这些动作可用于显式工作流。除 `file.hash`、`file.read` 外均需要写授权：

- `file.hash {root,path}` 返回 `size_bytes`、`sha256`、`version`。
- `file.read {root,path,offset,limit,expected_size,expected_version}` 返回 Base64 字节、`next_offset`、`eof`。不得把 Base64 当文本自动解码。
- `file.begin {root,path,size_bytes,sha256,overwrite}` 返回 `transfer_id` 和 `offset:0`。
- `file.write {root,transfer_id,offset,data_base64}` 返回 `next_offset`，每次最多 6144 字节，拒绝重复或跳跃 offset。
- `file.commit {root,transfer_id}` 校验并发布，返回文件哈希和长度。
- `file.abort {root,transfer_id}` 移除该传输的暂存文件，不修改目标。

所有返回包含 `root`；文件身份返回还包含 `path`，传输身份返回包含 `transfer_id`。源码检出中的 Windows 合成测试在 [`tests/windows/developer-files.ps1`](../tests/windows/developer-files.ps1)、[`tests/windows/developer-bootstrap.ps1`](../tests/windows/developer-bootstrap.ps1)；bootstrap 合成测试需要源码检出中的内嵌 loader 源文件；发行包用户直接使用二进制中的安装动作。主机 fake 测试不代表真实虚拟机或桌面验收。
