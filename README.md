# Qman / QQArchive — 媒体管理首版试用构建

Windows 中文桌面，Go＋Wails v2＋Vue 3/TypeScript＋SQLite。支持手选多个来源并切换、图片/视频/收到的表情按月份与大小查看、语音盘点、图片缩略预览及尝试视频播放、首次发现记录、目录包备份和独立 SHA-256 校验。所有 QQ 源文件只读，不登录、不注入、不上传，不删除或释放源空间。

运行 `build/bin/Qman.exe` 或新版 `build/bin/Qman-0.2.1.exe`；用户只需 WebView2 Runtime，不需要开发工具。说明见 [USER_GUIDE](docs/USER_GUIDE.md)，备份格式见 [MEDIA_FORMAT](docs/MEDIA_FORMAT.md)，真实覆盖与限制见 [PROGRESS](docs/PROGRESS.md)。支持选 Tencent Files 等外层文件夹发现账号，按账号切换；已有清单从 SQLite 分页显示，开始监控后增量补充，每 2 分钟完整核对。正常重开不自动读源媒体。

当前为合成测试与本机启动通信验证过的试用构建；真实 QQ 小样本、视频编码播放、实际窗口完整操作与大文件/断盘/容量故障/干净环境尚未验收，不称正式发行。消息解密继续暂缓，未解决旧 Reader 阻塞。

开发构建：Go 1.25+、Node/npm、WebView2，执行 `./tools/Build-Qman.ps1`，版本固定在 go.mod、go.sum、frontend/package-lock.json，工具和缓存放 `.local/`。开发源码首次编译需先构建前端资源。`go test ./...`、`go vet ./...` 运行合成测试；受限 Windows 宿主的发布权限失败须与普通 Windows 环境区分。

以下保留的是历史合成文本归档 CLI 开发说明，不是新媒体目录包的用法。

历史 CLI 是 **合成数据开发演示**。尚未适配真实 NTQQ，不是旧聊天归档 v0.1 的正式交付。

已有流程：生成合成明文 SQLite → 选择会话及日期 → 导出 `.qqarc` → 哈希/SQLite/计数/记录范围校验 → 源目录不可访问后打开与中文文字搜索。

归档默认包含明文。不会自动上传云盘，不支持恢复进 QQ，也不会删除或释放 QQ 源数据空间；创建副本和归档会增加占用。未知消息保留原始 JSON 与类型，结果带警告。

## 开发环境与运行

已运行环境：Windows amd64 / Go 1.25.4。项目最低 Go 1.25；SQLite 驱动固定 modernc.org/sqlite v1.39.1，依赖及校验值在 go.mod/go.sum。该纯 Go 驱动仅用于开发源和自有归档数据库，不用于解密 QQ。

在项目根目录执行：

```powershell
go test ./...
go build -o build/bin/qqarchive-cli.exe ./cmd/qqarchive-cli
```

以下全部是合成数据；目录必须不存在，示例先在用户临时目录建立独立父目录：

```powershell
$demoRoot = Join-Path $env:TEMP ('qqarchive-demo-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $demoRoot | Out-Null
go run ./cmd/qqarchive-cli demo (Join-Path $demoRoot 'source')
go run ./cmd/qqarchive-cli export (Join-Path $demoRoot 'source/source.db') (Join-Path $demoRoot 'demo.qqarc') demo-chat 2026-10-03 2026-10-03 Asia/Shanghai
go run ./cmd/qqarchive-cli verify (Join-Path $demoRoot 'demo.qqarc')
go run ./cmd/qqarchive-cli search (Join-Path $demoRoot 'demo.qqarc') 重复
```

目前 CLI 搜索一次最多 50 条；核心接口支持分页。日期按指定时区解释，包含结束日期，内部使用次日当地零点排他边界。ID 保留为字符串，重复文字不合并。

## 历史合成文本 CLI 的限制

- 只接受明确标记的合成 schema，不尝试打开 QQ 数据库。NTQQ 真实适配仍未完成，现已暂缓，不再是当前媒体管理前置。
- 没有图片、附件、catalog、图形界面、容量预览或恢复任务功能。
- 开发导入限制：SQLite 展开不超过 512 MiB，总展开不超过 1 GiB，元数据另有限额。这是演示资源上限，不是 v0.1 大包能力；超过 4 GiB 的 ZIP64 尚未实测。
- Windows 发布使用同卷 MoveFileExW，禁用覆盖和跨卷复制标志；其他平台暂用硬链接，尚未实测。移动存储、长路径与中断持久性仍需专项验收。
- cache 位于操作系统临时目录，仅用于解压 messages.db；当前还未完成持久用户 ACL/cache 容量管理，勿用来导入真实个人归档。
- Chinese 搜索使用参数化 LIKE 子串，不宣称 FTS5；分页不消除全表扫描成本。

真实副本不要放进仓库，不要把密钥粘贴到聊天。调查与证据见 docs/NTQQ_RESEARCH.md、docs/COMPATIBILITY.md 和 docs/PROGRESS.md。
