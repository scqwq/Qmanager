# Windows 消息库密钥获取：审查结论与实验步骤

日期：2026-10-07。目标版本为 QQ 9.9.26-44725（64 位）。用户指定日期为 2026-10-04，Asia/Shanghai，范围 [2026-10-03T16:00:00Z, 2026-10-04T16:00:00Z)。副本在 10 月 4 日创建，不能假定包含当天复制时刻之后的消息。

## 审查结论

只读数据库副本不直接提供已验证的消息密钥。当前没有找到对本版本有实证的纯离线取钥方案。QQ 登录密码、login.db 固定 key 和 VFS 扩展均不能替代消息库密钥。

| 候选 | 审查证据 | 当前决定 |
| --- | --- | --- |
| QQBackup/qq-win-db-key | PowerShell 源码 CreateProcessW(DEBUG_ONLY_THIS_PROCESS)、WriteProcessMemory(INT3)、TerminateProcess；NoDebugForKey 仅定位地址。教程确认版本 9.9.32-51246，非目标版本 | 不运行原取钥脚本，也不让用户直接远程执行它 |
| artiga033/ntdb_unwrap | Windows x86_64 源码创建调试进程、软件断点，KeyFound 后 terminate_process；README 称 Windows 实验性 | 不运行自动取钥/一键解密，候选 VFS 仍需 key |
| Will-hxw/QQLore | README 说明 QQDatabaseKey.exe 自动退出 QQ、重新登录后写 password.txt；树中存在该二进制，未找到足以核对它的对应取钥源码与构建证明 | 根 MIT 许可不等于该二进制已安全审计；不下载运行整套项目或 exe |
| Tealina28/QQNT_Export | 配置 db_path 明确要求解密数据库，根 GPLv3 | 可参考消息解析，不能解决取钥；未复制代码 |
| miniyu157/qq-dump | Android、root、sqlcipher，根 MIT | 不适用于本次 Windows 副本，移动端密钥不假定能解 Windows 库 |
| x64dbg 手动硬件断点 | 官方文档支持 debug-register 硬件断点与 detach；Windows x64 ABI 解释参数寄存器 | 可评估的实验路线，尚未在 QQ 实测；不承诺零风险或成功 |

公开资料读取时固定的 GitHub 分支树 SHA：qq-win-db-key b402e50133550efdff704e5a9921efbea5152bc1；ntdb_unwrap b1c5420a1957a833787b57f616243d4fa120526d；QQLore 8195c7f9c4c9c56b9eb5ae0a73fba52d2cf81425。没有导入上游代码。许可：qq-win-db-key 自定义非商用；ntdb_unwrap MIT；QQLore 根 MIT（不替代第三方资产审查）；x64dbg GPLv3，拟独立使用，不嵌入产品。

## 已完成的目标版本静态准备

新增独立工具 cmd/qqarchive-peprobe：标准 Go debug/pe 读取安装文件，搜索明确标记和 LEA RDX RIP 相对引用，结合 AMD64 unwind 表报告候选函数范围。无进程 API、网络、执行安装模块、写入安装文件或取钥能力。最大文件 256 MiB；不是通用反汇编器，字节匹配必须人工核对指令边界和函数语义。

目标目录 9.9.26-44725 的 wrapper.node 实际结果：

- SHA-256：74a5160067339a10b9d5dc3dc10b0e9aafc3837dab02b84f7f73fe192f98b801。
- 标记 nt_sqlite3_key_v2: db=%p zDb=%s 唯一。
- 引用 RVA：0x21B9795。
- unwind 表候选入口 RVA：0x21B9780；结束 RVA：0x21B97FE。
- 未获取实际加载地址或密钥，未附加/暂停/结束 QQ。

注册表卸载版本与安装子目录版本不同，不使用卸载项推断实际加载版本。上述 RVA 仅针对该文件哈希；模块不匹配时停止，禁止硬套地址。wrapper.node 内部 ProductVersion 不等于 QQ 客户端发布版本。

## 用户下一步与分阶段实验

当前可以先从 [x64dbg 官网](https://x64dbg.com/) 的 Download 下载官方便携包，解压到 QQ 安装目录以外。使用 x64 版本，无需关闭安全软件、提权服务、插件或 Frida。先不要附加 QQ；告知解压完成后核对包来源及工具版本。

若接受调试造成暂停、崩溃或账号风控的不确定性，后续由用户明确启动连接流程；不以虚拟机等同于消除账号风险。若必须零账号风险，当前方案无法满足，不开始动态操作。此处选择源自用户原有账号风险要求，非平台禁止其本人数据取钥。

实验顺序：

1. 核实仍为上述安装模块和哈希，保存手上的旧副本；不更新/降级 QQ。用户正常退出登录后保留登录界面，不强制终止任何进程。
2. 在 x64dbg 中选取加载了该 wrapper.node 的 QQ 进程。附加会影响运行；核对实际模块，而非随意选择第一个 QQ.exe。
3. 人工核对候选入口反汇编。运行地址 = 实际模块基址 + RVA；RVA 不是绝对地址。只为已确认入口设置执行类型的硬件断点，避免以默认软件断点替代。不启用隐藏调试器、反检测、补丁或注入插件。
4. 继续运行，用户正常登录自己的目标账号。若无法触发，不改源库、不强行关闭 QQ、不绕过反调试保护；停止实验并保留诊断。
5. 在正确的设钥函数入口观察第三/第四参数。Windows x64 通常对应 R8/R9D；只有在函数签名及语义核实后才读取 R8 指向的 nKey 字节，不能靠“16 个字符”猜对。不得把 login.db 的固定密钥当作消息 key。
6. 清除本次硬件断点，使用调试器 detach 解除附加，再确认 QQ 继续正常运行；不点击会结束目标的 Stop 或直接关闭正在附加的调试器。
7. 候选密钥不发到聊天、不截进公开截图、不放仓库/普通配置。后续只在本机隐藏输入中使用，通过旧 nt_msg.db 副本的兼容只读打开及结构查询确认正确。未经成功解密验证，只能称候选 key。

只有获取并验证 key 后，才验证 schema、WAL、目标会话、日期与图片映射。上述步骤是待实测的路线，不是已验证教程；现在没有运行动态实验，也没有可用 key。

## 主要依据

- [QQDecrypt Windows](https://qqbackup.github.io/QQDecrypt/decrypt/extract/NTQQ%20(Windows).html)
- [PowerShell 取钥源码](https://github.com/QQBackup/qq-win-db-key/blob/b402e50133550efdff704e5a9921efbea5152bc1/scripts/windows/ntqq/windows_ntqq_get_key.ps1)
- [ntdb_unwrap Windows 源码](https://github.com/artiga033/ntdb_unwrap/blob/b1c5420a1957a833787b57f616243d4fa120526d/src/ntqq/windows/debug_process_x86_64.rs)
- [QQLore README](https://github.com/Will-hxw/QQLore/blob/8195c7f9c4c9c56b9eb5ae0a73fba52d2cf81425/README.md)
- [QQNT_Export](https://github.com/Tealina28/QQNT_Export)
- [qq-dump](https://github.com/miniyu157/qq-dump)
- [x64dbg 硬件断点](https://help.x64dbg.com/en/latest/commands/breakpoint-control/SetHardwareBreakpoint.html)、[解除附加](https://help.x64dbg.com/en/latest/commands/debug-control/DetachDebugger.html)
- [微软 PE 格式](https://learn.microsoft.com/en-us/windows/win32/debug/pe-format)、[Windows x64 参数约定](https://learn.microsoft.com/en-us/cpp/build/x64-calling-convention?view=msvc-170)
