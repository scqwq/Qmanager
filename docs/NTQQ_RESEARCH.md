# NTQQ Reader 与密钥来源调查

日期：2026-10-07。目标版本：Windows 11 / NTQQ 9.9.26-44725（64 位，用户报告）。

本记录是公开文档和代码审查，不是目标版本适配证明。本轮 QQ 可保持运行，因为不访问源数据库或媒体，仅尝试检查已有副本。

## 已知实测与本轮限制

- 2026-10-04 完成 30 个数据库及伴随文件的隔离复制，源读前/读后与副本 SHA-256 相同，目录条目集合未变化。未复制媒体；不证明跨数据库业务一致性或媒体完整。
- 消息数据库副本在普通 SQLite 只读打开下返回 SQLITE_NOTADB（26）；没有真实消息或图片解析结果。
- 2026-10-07 重验副本时发生访问拒绝。原复制工具禁用权限继承，仅授权当时执行账户；执行身份变化后，用户与当前环境不能读取。访问拒绝不能解释为副本已删除或内容损坏。
- 已修正复制工具：要求明确 OwnerAccount，同时授权该用户及执行账户，保留父目录中特定 AppContainer 包/能力权限；不保留宽泛用户组。合成复制、读取与权限检查通过。旧副本未恢复访问，不能声称本轮哈希重验通过；未重新复制运行中的源库。
- 当前只保留数据库副本。需要源图片时须另行验证媒体读取稳定性，不能称已有完整离线样本。

## 公开候选方案

### sqlite_ext_ntqq_db / ntdb_unwrap

- 仓库：https://github.com/artiga033/ntdb_unwrap
- 当前查看的 crate 版本：sqlite_ext_ntqq_db 0.2.0；Cargo.toml 固定 libsqlite3-sys =0.36.0。
- 源码：https://github.com/artiga033/ntdb_unwrap/blob/main/sqlite_extension/src/offset_vfs.rs
- 许可证：仓库根 LICENSE 与 crate 声明 MIT。依赖和发布二进制仍须分别审计；本轮未复制上游代码、下载二进制、加载扩展或增加项目依赖。
- README 的示例仍要求 SQLCipher 及数据库密钥。VFS 处理文件头偏移，不能生成或替代密钥。
- 代码观察：按头部标记识别候选 NTQQ 格式，符合时使用固定偏移；此规则属于上游实现，不能直接作为目标版本事实。
- 代码观察：xWrite、xTruncate、xDelete 和 shared-memory 操作转发到底层实现。xDelete 注释写着拒绝删除，但函数实际转发。xOpen 传递调用方 flags，并不强制只读。因此这个扩展本身不是源数据只读防护。
- 代码观察：扩展注册为默认 VFS。若用于 QQArchive，应考虑隔离 helper，避免影响归档与 catalog 的普通 SQLite。
- 非主数据库文件打开直接转发，不足以证明目标版本 WAL 的解密与一致性已适配。
- ntdb_unwrap 的 Windows 自动取钥路径标为实验性，README 表明会启动新 QQ 进程、要求登录、进行二进制分析和动态调试。本轮不执行。
- 决策：保留为有密钥后评估的候选 Reader，不直接引入主程序，不作为密钥来源。

### qq-win-db-key / QQDecrypt

- Windows 教程：https://qqbackup.github.io/QQDecrypt/decrypt/extract/NTQQ%20(Windows).html
- 脚本：https://github.com/QQBackup/qq-win-db-key/blob/master/scripts/windows/ntqq/windows_ntqq_get_key.ps1
- 当前上游教程声明验证 9.9.32-51246，不是目标版本。
- 已审查脚本行为：默认调试启动 QQ，写软件断点，并在得到 key 后请求终止进程。NoDebugForKey 只进行静态地址定位，不输出实际 key。
- 决策：不执行该自动取钥路径。自动结束 QQ 与 AGENTS.md 的约束冲突，也未解决用户对主账号风险的要求。
- 仓库根 LICENSE 为自定义非商用许可，不能按通用宽松许可证直接复用；本轮未复制其代码。

### login.db 固定密钥线索

- 资料：https://qqbackup.github.io/QQDecrypt/database/db_file_analysis/login/index.html
- 文档区分登录缓存库密钥和账号消息库密钥；前者不能直接当作后者。
- 登录缓存可能包含登录票据。当前不为试错读取登录票据，也不调用 QQ 网络协议取得聊天库密钥。
- 决策：不把公开 login.db 密钥作为 nt_msg.db 的解决方案。

### Linux 离线推导线索

- 资料：https://qqbackup.github.io/QQDecrypt/research/NTQQ-Linux-DB-Key-Research.html
- 页面明确标注为 AI 生成研究笔记，平台与目标不同，且包含依赖服务端交互的讨论。不能作为 Windows 离线密钥推导的可靠证据。
- 决策：尚未找到已审计、适用于目标版本、只凭现有副本即可得到密钥的实证方案；这不证明此类方案绝对不存在。

## 后续验证条件

1. 恢复原私有副本的用户访问，重新核对证据清单；不得为此触碰 QQ 源目录或扩大到任意用户授权。
2. 固定副本的精确测试时间边界；副本不会随新消息更新。
3. 取得符合边界的密钥来源，或在明确独立的实验环境中单独研究；没有此条件时真实消息读取保持阻塞。
4. 有密钥后验证加密配置、schema 指纹、WAL 可见性和源侧无写入，而不是直接套用 README 参数。
5. 核对指定会话、发送者、顺序、时间、正文与图片；文本读取不能替代图片关联验证。

合成 Reader、归档格式和独立查看可以并行推进，但任何结果应标为开发演示，不能将真实 NTQQ 兼容项打勾。

## 权限恢复后的补充实测

2026-10-07：旧副本逐文件权限修复后，全部 30 文件大小及 SHA-256 重验通过。只读文件句柄读取 nt_msg.db 前 1056 字节；偏移 32 的 QQ_NT DB 标记匹配候选上游规则，0/1024 偏移均非标准 SQLite 头。未打印文件原始字节、未做解密、未访问运行中的源库。这是头部识别证据，不是 Reader 兼容性或密钥正确性的证据。此前权限阻塞记录保留为历史，当前已解除。

## 专项取钥交接

本轮方法比较、上游版本固定、目标版本静态准备和用户阶段步骤见 KEY_ACQUISITION.md。当前仅识别到安装模块唯一候选入口，没有可用密钥，也没有运行调试。
