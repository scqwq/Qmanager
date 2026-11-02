import type {BackupRecord} from './api'

export function hasTime(value?:string){return !!value&&!value.startsWith('0001-')&&!Number.isNaN(Date.parse(value))}
export const fmtTime=(value?:string)=>hasTime(value)?new Date(value!).toLocaleString():'时间未知'
export interface Presence {symbol:string;tone:string;description:string}

export function packagePresence(row:BackupRecord):Presence {
 const checked=`位置检查：${fmtTime(row.location_checked_at)}。`
 switch(row.local_state){
  case 'present': return row.status==='deleted'
   ?{symbol:'!',tone:'warning',description:`登记位置重新出现，需要重新校验。${checked}`}
   :{symbol:'√',tone:'success',description:`登记位置存在，仅检查位置，尚未校验包内内容。${checked}`}
  case 'missing':return {symbol:'×',tone:'muted',description:`登记位置未找到，直接父目录可访问；不搜索移走的副本。${checked}`}
  case 'unavailable':return {symbol:'?',tone:'warning',description:`位置暂不可访问，可能断盘或权限不足，不能判定丢失。${checked}`}
  case 'changed':return {symbol:'!',tone:'error',description:`登记位置的对象类型发生变化，需要核对。${checked}`}
  case 'pending':return {symbol:'!',tone:'warning',description:`备份包删除未完成，存在待处理暂存。${checked}`}
  default:return {symbol:'—',tone:'muted',description:'尚未检查备份包登记位置。'}
 }
}

export function originalPresence(row:BackupRecord):Presence {
 const result=row.original_files
 if(!result)return {symbol:'—',tone:'muted',description:'尚未检查本地源文件。点击“核对原文件”按这份备份的清单检查。'}
 const detail=`上次检查：${fmtTime(result.checked_at)}。存在 ${result.present}，未找到 ${result.missing}，大小/类型变化 ${result.changed}，未知 ${result.unknown}。仅路径与大小检查，不校验内容。`
 if(result.stale)return {symbol:'?',tone:'warning',description:`操作后需重新检查，旧结果不代表当前状态。${detail}`}
 switch(result.state){
  case 'present':return {symbol:'√',tone:'success',description:`清单中的源文件在上次检查时全部存在。${detail}`}
  case 'missing':return {symbol:'×',tone:'muted',description:`清单中的源文件在上次检查时全部未找到。${detail}`}
  case 'partial':return {symbol:'!',tone:'warning',description:`源文件部分存在、缺失或大小发生变化。${detail}`}
  case 'empty':return {symbol:'—',tone:'muted',description:'这份备份没有已成功备份的源文件条目。'}
  case 'unlinked':return {symbol:'?',tone:'warning',description:'未关联本机原来源，不能判断源文件是否存在。'}
  default:return {symbol:'?',tone:'warning',description:`部分或全部无法检查，不能当作文件缺失。${detail}`}
 }
}

export function cleanupLabel(row:BackupRecord){
 const result=row.cleanup_result
 if(!result)return row.source_cleanup_job?'有回收任务记录':'尚未回收'
 if(result.state==='preparing')return '正在检查待回收文件'
 if(result.state==='prepared')return result.remaining?'已检查，等待你确认回收':'检查完成，无可回收文件'
 if(result.state==='running')return '正在移入回收站'
 if(result.state==='interrupted')return '回收已停止'
 if(result.state==='cancelled')return result.recycled?'回收已停止':'已取消，未回收文件'
 if(result.failed||result.uncertain||result.remaining)return '回收未全部完成'
 if(result.skipped)return result.recycled?'已回收部分，其余跳过':'未回收，所选文件已跳过'
 return result.recycled?'回收完成':'任务结束，未回收文件'
}

export function cleanupCounts(row:BackupRecord){
 const result=row.cleanup_result
 if(!result||result.state==='preparing')return ''
 if(result.state==='prepared')return result.remaining?`待回收：${result.remaining} 个文件`:''
 const parts:string[]=[]
 if(result.recycled||result.state==='running')parts.push(`已回收：${result.recycled} 个`)
 if(result.remaining)parts.push(`尚未执行：${result.remaining} 个`)
 if(result.skipped)parts.push(`跳过：${result.skipped} 个`)
 if(result.failed)parts.push(`失败：${result.failed} 个`)
 if(result.uncertain)parts.push(`待核对：${result.uncertain} 个`)
 return parts.join(' · ')
}

const restoreLabels:Record<string,string>={running:'正在加载',completed:'已补回目标媒体目录',qman_only:'已保存到 Qman 独立库',fallback:'部分或全部保存到 Qman',partial:'部分完成，需查看结果',failed:'加载失败',cancelled:'已取消，可能保留部分文件',interrupted:'上次加载中断，结果待核对'}
export function restoreLabel(row:BackupRecord){
 const result=row.restore_result
 if(!result)return '尚未加载备份'
 if(result.state==='legacy'||result.state==='recorded')return hasTime(result.started_at)||hasTime(result.finished_at)?'有加载记录':'有加载记录 · 时间未知'
 return restoreLabels[result.state]??'结果待核对'
}
export function restoreTimeText(row:BackupRecord){
 const result=row.restore_result
 if(hasTime(result?.started_at))return `加载时间：${fmtTime(result!.started_at)}`
 if(hasTime(result?.finished_at))return `结束时间：${fmtTime(result!.finished_at)}`
 return ''
}
