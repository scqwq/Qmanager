import type {State} from './api'

export function coverageExplanation(value:string){
 if(/停顿|时钟/.test(value))return '电脑曾长时间停顿或系统时间改变，期间的文件变化可能没有完整记录。'
 if(/通知异常|积压|溢出/.test(value))return '文件变化通知异常或来得太多，可能漏掉了部分变化。'
 if(/未能写入/.test(value))return '部分文件变化没有成功保存到应用记录。'
 if(/来源暂不可访问/.test(value))return '媒体目录暂时无法访问，请检查磁盘连接和目录位置。'
 if(/未成功监听|未完整监听/.test(value))return '部分媒体文件夹没有成功开始监控。'
 if(/未完成核对|未能完整核对/.test(value))return '有些媒体文件还没有检查完。'
 return value
}

export function libraryStatus(state:State){
 const gap=state.needs_full?coverageExplanation(state.coverage)||'部分文件变化可能没有完整记录。':''
 if(['preparing_backup','preparing_delete'].includes(state.phase)){
  const deleting=state.phase==='preparing_delete'
  const files=deleting?'待回收文件':'备份文件'
  const title=state.check_stage==='stability'?`正在核对${files}是否停止变化（第 ${state.check_pass||1} 轮）`:state.check_stage==='hashing'?'正在核对待回收文件的内容，尚未删除':`正在准备本次所选${files}`
  const description=state.check_waiting?'正在短暂等待，随后再次核对所选文件；可以取消任务。':deleting?'只检查本次所选文件，自动补足稳定性核对；完成后仍需确认才会回收。':'只检查本次所选文件，自动补足稳定性核对后再复制；变化或不可读的项目会说明原因。'
  return {title,description,gap,tone:gap?'warning':'info',working:true}
 }
 const tasks:Record<string,string>={backup:'正在复制并校验备份',verifying:'正在检查所选文件',discovering:'正在查找账号媒体目录',restoring:'正在加载并保存媒体',deleting:'正在处理所选备份包',recycling:'正在将确认的文件移入回收站'}
 if(tasks[state.phase])return {title:tasks[state.phase],description:'',gap,tone:gap?'warning':'info',working:true}
 if(state.phase==='scanning')return {title:state.update_mode==='full'?'正在检查当前账号的全部受支持媒体':'正在补充新文件',description:'可以继续浏览已有清单；终止更新会保留已经保存的记录。',gap,tone:gap?'warning':'info',working:true}
 if(state.background_stage)return {title:state.background_stage==='stability'?'正在检查新发现或变化的文件':state.background_stage==='changes'?'正在检查发生变化的路径':'正在定期查找新文件',description:'这是后台检查，可以继续浏览和筛选。',gap,tone:gap?'warning':'info',working:false}
 if(state.mode==='backup')return {title:'正在查看备份媒体',description:'显示所选备份或此前已加载的媒体记录，不监控 QQ 源目录。',gap:'',tone:'info',working:false}
 if(gap)return {title:'有些文件变化可能没有记录',description:'建议手动进行一次全盘更新，检查遗漏、变化和缺失。不会自动开始全盘更新。',gap,tone:'warning',working:false}
 if(state.report&&!state.report.complete)return {title:state.report.cancelled?'这次检查已停止，已有清单已保留':'有些文件还没有检查完',description:'清单仍可浏览；未检查到的部分不能当作不存在，请查看下方问题明细。',gap:'',tone:'warning',working:false}
 if(state.cached)return {title:'已载入上次保存的媒体清单',description:'尚未检查当前文件是否有变化；需要时点击快速更新或全盘更新。',gap:'',tone:'info',working:false}
 if(state.watching)return {title:'正在监控新文件和变化',description:'每 2 分钟补充新文件；旧文件如有删除或替换，请使用全盘更新检查。',gap:'',tone:'info',working:false}
 if(state.report)return {title:'监控已暂停，媒体清单可继续浏览',description:'需要继续补充新文件时点击快速更新；全面检查旧文件请点击全盘更新。',gap:'',tone:'info',working:false}
 return {title:'还没有建立媒体清单',description:'点击快速更新开始；首次需要读取文件信息，完成前也可终止。',gap:'',tone:'info',working:false}
}
