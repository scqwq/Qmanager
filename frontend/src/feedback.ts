export type FeedbackTone='info'|'success'|'warning'|'error'
export type FeedbackPage='media'|'history'|'settings'
export interface Feedback {title:string;detail:string;advice?:string;tone:FeedbackTone;page?:FeedbackPage;at?:string}

export function errorFeedback(value:unknown):Feedback {
 const detail=String(value),text=detail.replace(/^Error:\s*/,'')
 let title='这次操作没有完成',advice='展开具体原因，检查后再重试。',tone:FeedbackTone='error'
 if(text.length<=48&&/^请[\u3400-\u9fff，。；：、（）！？…\s]+$/.test(text)){title=text;advice=''}
 if(/context canceled|已取消|用户取消/i.test(text)){title='操作已取消';advice='已经完成的部分可能保留，请查看本次操作结果。';tone='warning'}
 else if(/开始日期不能晚于结束日期/.test(text)){title='日期范围填写反了';advice='开始日期应早于或等于结束日期。'}
 else if(/当前任务.*(未结束|尚未结束)|当前任务尚未结束/.test(text)){title='上一项操作还在进行';advice='等它完成，或取消当前任务后再试。'}
 else if(/检测到 QQ 仍在运行/.test(text)){title='检测到 QQ 仍在运行';advice='目前不区分账号，请完全退出后回收。'}
 else if(/退出\s*QQ|QQ.*(运行|进程|退出)/.test(text)){title='请先确认 QQ 已退出';advice='手动完全退出 QQ 后重新检查；无法确认进程状态时，不会执行源文件操作。'}
 else if(/重叠|备份.*与.*分开|保存.*与.*分开/.test(text)){title='请换一个备份保存位置';advice='备份位置须与 QQ 数据目录、Qman 应用数据和已有来源分开。'}
 else if(/索引.*(容量|空间).*(不足|上限)|索引容量/.test(text)){title='应用记录空间不足';advice='先在设置中备份应用记录，再维护索引；不要直接删除数据库。'}
 else if(/空间不足|容量不足|not enough space|disk.*full/i.test(text)){title='保存位置的空间不足';advice='请选择空间足够的保存目录，再重新开始。'}
 else if(/不支持.*(备份|格式|版本)|格式.*不受支持|不受支持.*版本/.test(text)){title='暂不支持这份备份的格式';advice='请选择当前版本支持的完整备份；不会自动修改原备份。'}
 else if(/实例.*变化|身份.*变化|文件.*变化|内容.*变化|缓存.*不符/.test(text)){title='文件发生了变化，需要重新检查';advice='重新核对并选择本次要操作的文件，再继续。'}
 else if(/校验.*(失败|不符)|摘要.*不符|清单.*无效|备份.*无效/.test(text)){title='这份备份没有通过检查';advice='请确认选中了完整的备份；本次不会继续使用未通过校验的内容。'}
 else if(/占用|sharing violation|being used by another/i.test(text)){title='文件正被其他程序使用';advice='等待其他程序完成操作后重试。'}
 else if(/无法访问|不可访问|权限|拒绝访问|access.*denied/i.test(text)){title='暂时无法访问这个位置';advice='确认磁盘已连接、文件夹可访问，或重新选择位置。'}
 else if(/不存在|未找到|找不到|no such file|cannot find|could not find/i.test(text)){title='找不到需要的文件或文件夹';advice='检查磁盘连接，以及文件是否被移动、改名或删除。'}
 return {title,detail,advice,tone}
}

function count(text:string,label:string){return Number(text.match(new RegExp(`${label}\\s*(\\d+)`))?.[1]??0)}
function outstanding(text:string){return ['失败','待核对','未完成','未处理'].some(label=>count(text,label)>0)||Number(text.match(/(\d+)\s*项未完成/)?.[1]??0)>0||/未全部|部分完成/.test(text)}
export function resultFeedback(detail:string):Feedback|null {
 if(!detail||/^已从本地索引加载|^变化路径已核对/.test(detail))return null
 const result:Feedback={title:'操作结果',detail,tone:'info'}
 if(detail.startsWith('已核对本份备份'))return {...result,page:'history',title:'本地源文件状态已更新',advice:'查看对应记录中的检查时间与数量；这里只检查路径和大小。'}
 if(detail.startsWith('已删除该备份的历史记录'))return {...result,page:'history',tone:'success',title:'已删除此条记录',advice:'原文件、已加载媒体和独立操作结果仍然保留。'}
 if(detail.startsWith('备份删除操作结束'))return {...result,page:'history',tone:outstanding(detail)?'warning':'success',title:outstanding(detail)?'部分本地备份包尚未处理完成':'本地备份包删除操作已结束',advice:'逐项结果已保存在备份历史中。'}
 if(detail.startsWith('已核对并固定'))return {...result,title:'待回收文件已检查，等待确认',advice:'此时尚未删除文件，请先查看本次固定清单。'}
 if(detail.startsWith('该删除任务已经结束'))return {...result,title:'这项回收任务已经结束',advice:'没有重复执行回收，可查看原任务结果。'}
 if(detail.startsWith('删除结束'))return {...result,tone:outstanding(detail)||count(detail,'跳过')?'warning':'success',title:'源文件回收已结束',advice:'已回收、跳过和未完成的数量请查看任务结果；回收站没有被清空。'}
 if(detail.startsWith('全盘更新完成'))return {...result,page:'media',title:'全盘更新已结束',advice:'检查范围和问题见下方状态栏；之后继续快速监控。'}
 if(detail.startsWith('快速更新完成'))return {...result,page:'media',title:'已补充新文件',advice:'旧文件的变化和缺失尚未全面检查，需要时请使用全盘更新。'}
 if(detail.startsWith('更新已终止'))return {...result,page:'media',title:'更新已停止，已有清单已保留',advice:'后台监控也已暂停；需要时点击快速更新。'}
 if(detail.startsWith('已发现并保存'))return {...result,page:'media',title:'已找到并保存媒体来源',advice:'在左侧选择账号，再点击快速更新建立清单。'}
 if(detail.startsWith('备份已完成'))return {...result,page:'media',tone:'success',title:'备份已完成，校验通过',advice:'监控已暂停；需要继续监控时点击快速更新。'}
 if(detail.startsWith('备份校验通过'))return {...result,page:'media',tone:'success',title:'备份已打开，校验通过',advice:'已读取包内保存的观察记录；源目录断开后也可查看这份备份。'}
 if(detail.includes('补回')&&detail.includes('仅 Qman')){
  const fallback=count(detail,'仅 Qman'),written=count(detail,'补回')
  return {...result,page:'media',tone:outstanding(detail)?'warning':fallback?'info':'success',title:outstanding(detail)?'备份没有全部加载完成':fallback&&!written?'文件已保存到 Qman 独立库':fallback?'加载已结束，部分文件保存到 Qman':'备份加载已完成',advice:'具体数量和保存位置可在备份历史中查看。'}
 }
 return {...result,tone:outstanding(detail)||/失败|异常|无法|中断|缺口/.test(detail)?'warning':'info'}
}
