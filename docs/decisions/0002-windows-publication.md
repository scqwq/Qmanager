# Windows 归档发布不覆盖

日期：2026-10-07。

原开发实现用硬链接发布包，受限环境实测失败。Windows 改用 MoveFileExW；暂存目录在最终输出父目录下，调用不启用 REPLACE_EXISTING 或 COPY_ALLOWED。已有目标即失败，禁止退回到覆盖式 Rename。其他平台保留同卷硬链接原型，未实测。

依据：[微软 MoveFileExW 文档](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw)，以及固定 golang.org/x/sys v0.36.0 的 Windows 封装源码。x/sys 已是 SQLite 间接依赖，本次改为直接依赖，不新增版本；许可证 BSD-3-Clause。

WRITE_THROUGH 不等于所有设备上都具备断电持久性。移动盘、长路径、跨卷和中断验收仍待完成。不启用跨卷复制，避免复制成功但原暂存保留等行为被误认为发布完成。

直接发布测试确认已有文件与暂存文件内容均未改变；普通 Windows 权限下合成归档测试通过，受限环境仍返回 Access denied。
