<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {api,type BackupEvent,type BackupRecord,type Diagnostics} from '../api'
import {cleanupCounts,cleanupLabel,fmtTime,restoreLabel} from '../backupPresentation'
import FeedbackNotice from './FeedbackNotice.vue'
import RecordPager from './RecordPager.vue'
import {errorFeedback} from '../feedback'

const props=defineProps<{backupId?:string}>()
defineEmits<{back:[]}>()

const data=ref<Diagnostics|null>(null)
const records=ref<BackupRecord[]>([])
const events=ref<BackupEvent[]>([])
const observation=ref<BackupRecord|null>(null)
const selectedID=ref(props.backupId??'')
const loading=ref(false),eventLoading=ref(false),error=ref(''),eventError=ref('')
const filter=ref('all')
const crashOpen=ref(!props.backupId),backupsOpen=ref(!!props.backupId),runOpen=ref(false)
const crashPage=ref(1),backupPage=ref(1),runPage=ref(1),eventPage=ref(1)
const crashFrom=ref(''),crashTo=ref(''),backupFrom=ref(''),backupTo=ref(''),runFrom=ref(''),runTo=ref('')
type Removal={kind:'crash'|'system'|'run';id:string;at:string}
const pendingRemoval=ref<Removal|null>(null),removing=ref(false)
let disposed=false,eventGeneration=0

function localDate(value:string){
 const date=new Date(value)
 const pad=(n:number)=>String(n).padStart(2,'0')
 return `${date.getFullYear()}-${pad(date.getMonth()+1)}-${pad(date.getDate())}`
}
function between(value:string,from:string,to:string){
 const day=localDate(value)
 return (!from||day>=from)&&(!to||day<=to)
}
function pageOf<T>(items:T[],page:number,size:number){return items.slice((page-1)*size,page*size)}
const pages=(count:number,size:number)=>Math.max(1,Math.ceil(count/size))
const crashes=computed(()=>{
 const system=(data.value?.crashes??[]).map(item=>({...item,kind:'system' as const,title:item.app,body:`异常代码 ${item.code} · ${item.module} · 位置 ${item.offset}`}))
 const local=(data.value?.crash_text??[]).map(item=>({...item,kind:'crash' as const,title:'程序崩溃',body:item.text}))
 return [...system,...local].filter(item=>between(item.at,crashFrom.value,crashTo.value)).sort((a,b)=>Date.parse(b.at)-Date.parse(a.at))
})
const backups=computed(()=>records.value.filter(row=>between(row.created_at,backupFrom.value,backupTo.value)).sort((a,b)=>Date.parse(b.created_at)-Date.parse(a.created_at)))
const entries=computed(()=>(data.value?.entries??[]).filter(item=>(filter.value==='all'||item.level!=='info')&&between(item.at,runFrom.value,runTo.value)))
const selected=computed(()=>records.value.find(row=>row.id===selectedID.value))

watch([crashFrom,crashTo],()=>{crashPage.value=1})
watch([backupFrom,backupTo],()=>{backupPage.value=1})
watch([runFrom,runTo,filter],()=>{runPage.value=1})
watch(crashes,list=>{crashPage.value=Math.min(crashPage.value,pages(list.length,5))})
watch(entries,list=>{runPage.value=Math.min(runPage.value,pages(list.length,5))})
watch(backups,list=>{backupPage.value=Math.min(backupPage.value,pages(list.length,6))})

async function refresh(){
 if(loading.value)return
 loading.value=true
 error.value=''
 const [diagnostics,index]=await Promise.allSettled([api().GetDiagnostics(),api().GetBackupLogIndex()])
 if(disposed)return
 if(diagnostics.status==='fulfilled')data.value=diagnostics.value
 else error.value=String(diagnostics.reason)
 if(index.status==='fulfilled')records.value=index.value
 else error.value=String(index.reason)
 loading.value=false
}
async function showEvents(id:string,page=1){
 const generation=++eventGeneration
 if(selectedID.value!==id)observation.value=null
 selectedID.value=id
 eventPage.value=page
 events.value=[]
 eventLoading.value=true
 eventError.value=''
 try{
  const value=await api().GetBackupEvents(id,(page-1)*20)
  if(!disposed&&generation===eventGeneration)events.value=value
 }catch(e){if(!disposed&&generation===eventGeneration)eventError.value=String(e)}
 finally{if(!disposed&&generation===eventGeneration)eventLoading.value=false}
 if(page===1){
  try{
   const value=await api().InspectBackupRecord(id,false)
   if(!disposed&&generation===eventGeneration)observation.value=value
  }catch{/* Operation records remain readable without a current observation. */}
 }
}
async function refreshSelected(){
 await refresh()
 if(selectedID.value&&!disposed)void showEvents(selectedID.value,eventPage.value)
}
async function remove(){
 const record=pendingRemoval.value
 if(!record||removing.value)return
 removing.value=true
 try{
  await api().EditDiagnosticRecord(record.kind,record.id)
  pendingRemoval.value=null
  await refresh()
 }catch(e){error.value=String(e);pendingRemoval.value=null}
 finally{removing.value=false}
}

onMounted(async()=>{
 await refresh()
 if(!props.backupId||disposed)return
 const index=backups.value.findIndex(row=>row.id===props.backupId)
 if(index>=0)backupPage.value=Math.floor(index/6)+1
 void showEvents(props.backupId)
})
onUnmounted(()=>{disposed=true;eventGeneration++})
</script>

<template>
 <section class="logs-page" aria-labelledby="logs-title">
  <button type="button" class="text-button logs-back" @click="$emit('back')"><span aria-hidden="true">‹</span> {{backupId?'返回备份历史':'返回设置'}}</button>
  <div class="logs-heading"><h2 id="logs-title">运行日志</h2><button type="button" class="secondary" :disabled="loading" @click="refreshSelected">{{loading?'正在读取…':'刷新'}}</button></div>
  <FeedbackNotice v-if="error" :feedback="errorFeedback(error)"/>
  <p v-if="data?.write_note" class="notice warning" role="status">{{data.write_note}}</p>

  <details class="logs-section" :open="crashOpen" @toggle="crashOpen=($event.target as HTMLDetailsElement).open">
   <summary>崩溃记录</summary>
   <div class="record-filters">
    <label>开始日期 <input v-model="crashFrom" type="date" :max="crashTo||undefined"></label>
    <label>结束日期 <input v-model="crashTo" type="date" :min="crashFrom||undefined"></label>
   </div>
   <p v-if="data?.system_note" class="logs-muted">{{data.system_note}}</p>
   <p v-if="!crashes.length&&!loading" class="logs-muted">此日期范围没有崩溃记录。</p>
   <div v-for="item in pageOf(crashes,crashPage,5)" :key="item.id" class="log-crash">
    <div class="record-line"><span>{{fmtTime(item.at)}} · {{item.title}}</span><button class="text-button" type="button" :disabled="loading" @click="pendingRemoval=item">{{item.kind==='system'?'隐藏':'删除'}}</button></div>
    <details><summary>错误详情</summary><pre>{{item.body}}</pre></details>
   </div>
   <RecordPager v-if="crashes.length>5" :page="crashPage" :total="pages(crashes.length,5)" :more="crashPage*5<crashes.length" @change="crashPage=$event"/>
  </details>

  <details class="logs-section" :open="backupsOpen" @toggle="backupsOpen=($event.target as HTMLDetailsElement).open">
   <summary>备份操作记录</summary>
   <div class="record-filters">
    <label>创建起始日 <input v-model="backupFrom" type="date" :max="backupTo||undefined"></label>
    <label>创建结束日 <input v-model="backupTo" type="date" :min="backupFrom||undefined"></label>
   </div>
   <p v-if="!backups.length&&!loading" class="logs-muted">此日期范围没有备份。</p>
   <div class="backup-log-choices">
    <button v-for="row in pageOf(backups,backupPage,6)" :key="row.id" class="secondary" type="button" :aria-pressed="row.id===selectedID" @click="showEvents(row.id)">
     <strong>{{row.name}}</strong><span>{{fmtTime(row.created_at)}} · {{row.files}} 个文件</span>
    </button>
   </div>
   <RecordPager v-if="backups.length>6" :page="backupPage" :total="pages(backups.length,6)" :more="backupPage*6<backups.length" @change="backupPage=$event"/>
   <div v-if="selected" class="backup-log-detail">
    <h3>{{selected.name}} · {{fmtTime(selected.created_at)}}</h3>
    <FeedbackNotice v-if="eventError" :feedback="errorFeedback(eventError)"/>
    <p v-if="eventLoading" role="status">正在读取操作记录…</p>
    <p v-else-if="!events.length&&!eventError" class="logs-muted">暂无逐项操作记录。</p>
    <ol v-if="events.length" class="log-entries">
     <li v-for="(item,index) in events.slice(0,20)" :key="index"><time :datetime="item.at">{{fmtTime(item.at)}}</time><div><strong>{{item.action}}</strong><p>{{item.detail}}</p></div></li>
    </ol>
    <RecordPager v-if="eventPage>1||events.length>20" :page="eventPage" :more="events.length>20" @change="showEvents(selectedID,$event)"/>
    <details v-if="selected.restore_result||selected.cleanup_result||selected.source_cleanup_detail||observation" class="backup-log-summary">
     <summary>最近核对与操作结果</summary>
     <p v-if="observation?.location_checked_at">备份包 · {{fmtTime(observation.location_checked_at)}} · {{observation.local_detail}}</p>
     <p v-if="observation?.original_files">本地源文件 · {{fmtTime(observation.original_files.checked_at)}} · 存在 {{observation.original_files.present}} · 缺失 {{observation.original_files.missing}} · 变化 {{observation.original_files.changed}} · 未知 {{observation.original_files.unknown}}</p>
     <p v-if="selected.restore_result">加载 · {{fmtTime(selected.restore_result.finished_at||selected.restore_result.started_at)}} · {{restoreLabel(selected)}}<template v-if="selected.restore_result.state!=='legacy'"><br>补回 {{selected.restore_result.written}} · 跳过 {{selected.restore_result.skipped}} · 冲突 {{selected.restore_result.conflicts}} · 仅 Qman {{selected.restore_result.fallback}} · 未完成 {{selected.restore_result.failed}}</template><template v-if="selected.restore_result.detail"><br>{{selected.restore_result.detail}}</template></p>
     <p v-if="selected.cleanup_result||selected.source_cleanup_detail">回收 · {{fmtTime(selected.cleanup_result?.at)}} · {{cleanupLabel(selected)}} {{cleanupCounts(selected)||selected.source_cleanup_detail}}</p>
    </details>
   </div>
  </details>

  <details class="logs-section" :open="runOpen" @toggle="runOpen=($event.target as HTMLDetailsElement).open">
   <summary>最近运行记录</summary>
   <div class="record-filters">
    <label>开始日期 <input v-model="runFrom" type="date" :max="runTo||undefined"></label>
    <label>结束日期 <input v-model="runTo" type="date" :min="runFrom||undefined"></label>
    <label>显示 <select v-model="filter"><option value="all">全部记录</option><option value="issues">仅异常与提醒</option></select></label>
   </div>
   <p v-if="!entries.length&&!loading" class="logs-muted">此日期范围没有运行记录。</p>
   <ol v-else class="log-entries">
    <li v-for="entry in pageOf(entries,runPage,5)" :key="entry.id" :class="{'log-warning':entry.level==='warning','log-error':entry.level==='error'}">
     <time :datetime="entry.at">{{fmtTime(entry.at)}}</time>
     <div class="log-entry-copy"><p>{{entry.detail}}</p><details><summary>定位信息</summary><p>阶段 <code>{{entry.event}}</code><template v-if="entry.task"><br>任务 <code>{{entry.task}}</code></template><template v-if="entry.item"><br>文件编号 <code>{{entry.item}}</code></template></p></details></div>
     <button class="text-button" type="button" :disabled="loading" @click="pendingRemoval={kind:'run',id:entry.id,at:entry.at}">删除</button>
    </li>
   </ol>
   <RecordPager v-if="entries.length>5" :page="runPage" :total="pages(entries.length,5)" :more="runPage*5<entries.length" @change="runPage=$event"/>
  </details>

  <div v-if="pendingRemoval" class="modal-backdrop" @click.self="!removing&&(pendingRemoval=null)">
   <section class="backup-modal" role="dialog" aria-modal="true" aria-label="确认处理日志记录" @keydown.esc.stop="!removing&&(pendingRemoval=null)">
    <h2>{{pendingRemoval.kind==='system'?'隐藏此记录？':'删除此记录？'}}</h2>
    <p>{{fmtTime(pendingRemoval.at)}}</p>
    <p v-if="pendingRemoval.kind==='system'">仅从 Qman 列表隐藏，Windows 事件仍保留。</p>
    <div class="modal-actions">
     <button class="secondary" type="button" :disabled="removing" @click="pendingRemoval=null">取消</button>
     <button :class="pendingRemoval.kind==='system'?'primary':'danger-button'" type="button" :disabled="removing" @click="remove">{{pendingRemoval.kind==='system'?'隐藏':'删除'}}</button>
    </div>
   </section>
  </div>
 </section>
</template>
