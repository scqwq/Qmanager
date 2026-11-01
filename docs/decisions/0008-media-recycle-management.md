# 0008 — 媒体页的显式选择与回收站删除

日期：2026-11-01。用户授权管理模式、小勾选框、删除及“2025 年所有收到的表情/2026 年 9 月语音”等条件批量选择，不限定先备份。默认使用 Windows 回收站，保留用户显式确认，不把备份状态当删除授权；不增加永久源删除或清空回收站。

前端选择分手选与“全部匹配 + 排除”，跨页保持，条件/来源变化清空。提交预检后按当前来源 SQL 筛选结果固定实例，不把确认后的新文件纳入；身份与内容 SHA-256 流式核对，未知/未稳定/临时/不可读项跳过，预检完成后 30 分钟内可明确确认。

SQLite cleanup_jobs/cleanup_items 记录清单与逐项结果，不写源侧标记。执行前与过程中核实 QQ 未运行，不强退客户端。使用原生 IFileOperation、FOFX_RECYCLEONDELETE/ADDUNDORECORD 和逐项确认；固定目录祖先、锁定写入并在 Shell 回调复核路径/身份/内容。Shell 绑定目标需要删除共享，不能以独占删除锁阻塞原生绑定；不宣称阻断所有外部并发变动或产生一致快照。没有永久删除 API 兜底，未确认回收站对象则保留失败/不确定，必要时停止余项。

成功项仅移除当前查询投影/标记实例不在场，原时间与历史保留，新增 user_recycled 事件。中断的 processing 记 uncertain，启动恢复只在获得单实例后的生命周期执行，不能每次开索引都打断另一进程的任务；不自动继续。保留最近任务查询，重复执行终态任务不再操作。

回收站仍占空间，文件逻辑长度不等于物理释放。备份内部对象去重共享，不提供逐对象管理删除；备份历史永久删包与源媒体回收分开。无源目录递归、云盘自动清理、客户端注入或消息库写入。

官方接口依据：
- https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nn-shobjidl_core-ifileoperation
- https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nf-shobjidl_core-ifileoperation-setoperationflags
- https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nf-shobjidl_core-ifileoperationprogresssink-postdeleteitem

真实大文件/长路径/回收容量不足/驱动器策略、崩溃全过程和真实 QQ/原生全程仍需专项验收；合成原生小样本不构成全面支持或账号零风险证明。
