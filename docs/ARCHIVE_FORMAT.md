> 2026-10-30 范围说明：本文保留为合成聊天文本包的历史开发格式；当前媒体优先方向尚未定义/实现独立媒体格式，不能将本格式当作媒体功能完成证据。

# `.qqarc` 开发格式实现记录

当前实现仅为 v1 容器的 **synthetic_plain 开发子集**，不是全部 v0.1 格式与大文件能力的验收。

## 已实现有效载荷

- manifest.json：格式 qqarchive、major=1、minor=0，UUID、版本、合成来源、源 schema SHA-256、账号/会话、时区和 UTC 半开范围、消息计数及内容状态。
- messages.db：普通 SQLite；messages 保留源定位键、字符串发送者 ID、UTC 原时间文本、源序号、消息类型、文本及原始 JSON；media_objects/media_references 为空，当前尚不支持媒体。
- metadata/conversation.json：会话 ID、名称快照、类型，与 manifest 核对。
- metadata/participants.json：发送者标识从 messages.db 获取，历史名称未知；当前为索引说明，不是完整参与者元数据。
- reports/export.json：明确 development_demo、内容警告及明文属性。导入不信任报告声明作为校验结论。
- checksums.json：所有其他有效载荷路径、展开字节数与 SHA-256，不包含自身。实际条目集合必须匹配；开发子集拒绝额外有效载荷。

标准 archive/zip 容器由 Go 库编码，文本/数据库用 DEFLATE。ZIP64 超大文件端到端测试尚未执行，不能称支持已验收。

## 打开与校验

拒绝危险路径、链接/非普通文件、重复条目、不支持主版本、缺失/多余清单条目、哈希不匹配、SQLite 损坏、视图/触发器/虚拟表与未知 schema 对象、计数不匹配、范围外或损坏消息。metadata/conversation 与 manifest 独立比对。

先校验条目与各载荷哈希，仅解压 messages.db 到应用创建的临时目录；只读 SQLite 并设置 query_only/trusted_schema，消息分页查询，Close 关闭连接并清理自己的临时目录。未实现 Windows cache ACL/磁盘容量预检，因此当前只允许开发演示资料。

界限：最多 10,000 个条目、元数据 manifest 1 MiB/checksums 2 MiB、单文件 512 MiB、总展开 1 GiB，展开大于 1 MiB 时压缩比超过 1000 拒绝。限制用于小型开发样本，未来大媒体必须另定可测策略。

整包哈希在 ZIP 关闭后计算，仅返回 CLI，不嵌回包内，不提供签名/来源认证或云端验证。
