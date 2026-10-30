# 开发归档核心先行

日期：2026-10-07。

真实 Reader 被密钥及兼容性阻塞；按 AGENTS.md M1 继续推进不依赖真实数据的模块。

选择：Go 核心 CLI、modernc.org/sqlite v1.39.1、标准 archive/zip。前者是已缓存且支持 Windows amd64 的纯 Go 驱动，可避免当前开发核心引入 CGO。版本锁定，不声称最新版。driver/libc 按上游要求保持一致；主库 BSD 风格许可，完整依赖声明仍需打包前审计与保留。

官方/上游证据：https://gitlab.com/cznic/sqlite/-/raw/v1.39.1/go.mod；本地 module 的 LICENSE/doc.go 已检查。Go 1.25.4 运行测试，而 driver 要求 Go >=1.24。

此依赖不承担 QQ 解密。候选 SQLCipher/VFS 应隔离研究，不能污染普通归档/catalog SQLite。回退条件：性能、内存、安全限制或 Windows 打包验证无法达标时再评价驱动；不因 UI 偏好重写。

本阶段限制：文本和未知消息；媒体模型空表，拒绝导入带媒体的包。先取得一条真实归档行为，不创建 Wails/Vue 空壳。未覆盖图片的结果不称 M2 完成。
