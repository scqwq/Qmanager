# 媒体优先与新增观察日期

日期：2026-10-30。用户明确要求先做图片、视频等本地媒体管理，暂缓聊天记录解密，并报告跨月下载仍落在旧月 Ori 的现象。

当前 AGENTS.md 改为 v0.2 媒体优先契约；旧约定、计划原样保存在 history，不删除真实 Reader 未完成的证据，不把范围调整当作旧版验收通过。已有合成文本 CLI 保留，新媒体格式不得伪造消息迎合旧 .qqarc。

采用来源月份与工具新增观察日期双维度。监控全部已选月份子树，基线区分旧文件，记录重命名、增长、稳定、替换与再次出现；事件去抖、有界队列、补扫和覆盖缺口必须有测试。稳定检查是启发式，不等于 QQ 下载确认。仅目录月份与当前月份对比不能证明下载时间。

记号存源外应用私有索引，不改文件内容、名称、属性、时间、ADS 或 QQ 配置。观察历史不能仅凭原目录重建，需索引备份，并随媒体备份保存所选对象的时间证据。工具关闭后重扫不能补造精确下载时刻；首次基线不把全部旧媒体算今天下载。

只读监控与源数据安全要求继续保留，无删除/移动授权、不注入/调试 QQ。当前未实现监控/标记，只完成文档契约。

依据：

- [微软 ReadDirectoryChangesW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-readdirectorychangesw)：子树事件、缓冲丢失及重新枚举。
- [微软 FileSystemWatcher](https://learn.microsoft.com/en-us/dotnet/api/system.io.filesystemwatcher)：重复事件、移动和通知不完整。
- [微软文件时间](https://learn.microsoft.com/en-us/windows/win32/sysinfo/file-times)：时间字段的文件系统语义与可修改性。

若后续存在经过验证的 QQ 下载完成 API 或用户主动提供时间，新增独立证据来源，不覆盖观察记录，也不把更高置信度强加给旧数据。清理和实际释放空间始终需要单独设计/验收。
