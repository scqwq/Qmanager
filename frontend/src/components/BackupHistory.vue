<script setup lang="ts">
import FeedbackNotice from './FeedbackNotice.vue'
import {errorFeedback} from '../feedback'
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {api,type BackupMarkState,type BackupPresenceKey,type BackupRecord} from '../api'
import HelpTip from './HelpTip.vue'
import BackupCloudUpload from './BackupCloudUpload.vue'
import {backupPresence,presenceKeys,presenceLabels} from '../backupPresentation'
const props=defineProps<{busy:boolean}>()
const emit=defineEmits<{load:[id:string];relocate:[id:string];view:[id:string];remove:[ids:string[]];forget:[id:string];operations:[id:string];clean:[ids:string[]];cleanResults:[jobID:string];cloudLogin:[]}>()
const refreshing=ref(false)
const opening=ref('')
const rows=ref<BackupRecord[]>([]),checked=ref<string[]>([]),error=ref(''),confirming=ref(false)
const forgetting=ref<BackupRecord|null>(null)
const editing=ref<BackupRecord|null>(null)
const upload=ref<BackupRecord|null>(null)
const markDraft=ref<Partial<Record<BackupPresenceKey,BackupMarkState>>>({})
const rowAction=ref('')
const canForget=(row:BackupRecord)=>row.status==='deleted'&&row.local_state==='missing'
const selectedRecords=computed(()=>rows.value.filter(r=>checked.value.includes(r.id)))
const selected=computed(()=>selectedRecords.value.filter(r=>r.status!=='deleted'||r.local_state==='present'||r.local_state==='pending'))
const cleanable=computed(()=>selectedRecords.value.filter(r=>r.files>0))
const allChecked=computed(()=>rows.value.slice(0,100).length>0&&rows.value.slice(0,100).every(r=>checked.value.includes(r.id)))
const partiallyChecked=computed(()=>checked.value.length>0&&!allChecked.value)
const selectedBytes=computed(()=>selected.value.reduce((sum:number,row:BackupRecord)=>sum+row.bytes,0))
const bytes=(v:number)=>`${(v/1024/1024).toLocaleString(undefined,{maximumFractionDigits:1})} MiB`
function scope(r:BackupRecord){if(r.selected_only)return '已选媒体';const f=r.selection;const names:Record<string,string>={image:'图片',video:'视频',emoji:'收到的表情',voice:'语音'};return `${names[f.kind]||'全部类型'} · ${f.month_from||f.month_to?`${f.month_from||'不限'} 至 ${f.month_to||'不限'}`:f.month||'全部月份'}${f.view==='observed'?' · 发现日期视图':''}${f.from||f.to?` · ${f.from||'不限'} 至 ${f.to||'不限'}`:''}`}
let loading=false,disposed=false,refreshAgain=false
const rowVersions=new Map<string,number>()
async function refresh(){
 if(disposed)return
 if(loading){refreshAgain=true;return}
 loading=true
 refreshing.value=true
 try{
  const result=await api().GetBackupHistory()
  if(disposed)return
  rows.value=result
  error.value=''
  refreshing.value=false
  for(const row of rows.value){
   if(disposed||props.busy)break
   if(rowAction.value===row.id)continue
   const version=rowVersions.get(row.id)
   try{
    const observed=await api().InspectBackupRecord(row.id,false)
    if(!disposed&&version===rowVersions.get(row.id))applyObservation(row.id,observed,false)
   }catch{
    if(!disposed&&version===rowVersions.get(row.id)){
     row.original_files={state:'unknown',checked_at:new Date().toISOString(),stale:false,total:row.files,present:0,missing:0,changed:0,unknown:row.files}
    }
   }
  }
 }catch(e){if(!disposed)error.value=String(e)}
 finally{
  loading=false
  refreshing.value=false
  if(refreshAgain&&!disposed){refreshAgain=false;void refresh()}
 }
}
function applyObservation(id:string,observed:BackupRecord,explicit:boolean){
 const row=rows.value.find(item=>item.id===id)
 if(!row)return
 row.original_files=observed.original_files
 row.local_state=observed.local_state
 row.local_detail=observed.local_detail
 row.location_checked_at=observed.location_checked_at
 if(explicit)row.display=observed.display
}
async function inspect(row:BackupRecord){
 if(props.busy||rowAction.value)return
 rowVersions.set(row.id,(rowVersions.get(row.id)??0)+1)
 rowAction.value=row.id
 error.value=''
 try{
  const observed=await api().InspectBackupRecord(row.id,true)
  if(!disposed)applyObservation(row.id,observed,true)
 }catch(e){if(!disposed)error.value=String(e)}
 finally{rowAction.value=''}
}
function edit(row:BackupRecord){
 markDraft.value={}
 editing.value=row
}
async function saveMarks(){
 const row=editing.value
 if(!row||rowAction.value)return
 const marks=Object.fromEntries(Object.entries(markDraft.value).filter(([,value])=>value==='present'||value==='missing'))
 if(!Object.keys(marks).length)return
 rowAction.value=row.id
 error.value=''
 try{
  const display=await api().MarkBackupRecord(row.id,marks)
  if(!disposed){
   const current=rows.value.find(item=>item.id===row.id)
   if(current)current.display=display
   editing.value=null
  }
 }catch(e){if(!disposed){error.value=String(e);editing.value=null}}
 finally{rowAction.value=''}
}
async function openLocation(row:BackupRecord){if(props.busy||opening.value)return;opening.value=row.id;error.value='';try{await api().OpenBackupLocation(row.id)}catch(e){if(!disposed)error.value=String(e)}finally{opening.value=''}}
function toggleAll(event:Event){checked.value=(event.target as HTMLInputElement).checked?rows.value.slice(0,100).map(r=>r.id):[]}
function remove(){const ids=selected.value.map(r=>r.id);confirming.value=false;checked.value=[];emit('remove',ids)}
function clean(){const ids=cleanable.value.map(r=>r.id);checked.value=[];emit('clean',ids)}
function forget(){const row=forgetting.value;if(!row||props.busy)return;forgetting.value=null;checked.value=checked.value.filter(id=>id!==row.id);emit('forget',row.id)}
watch(()=>props.busy,(current,previous)=>{if(previous&&!current)void refresh()})
function focusRefresh(){if(!props.busy)void refresh()}
onMounted(()=>{void refresh();window.addEventListener('focus',focusRefresh) });onUnmounted(()=>{disposed=true;window.removeEventListener('focus',focusRefresh)})
</script>
<template>
<section class="backup-history">
 <div class="history-toolbar"><button class="secondary" :disabled="busy||refreshing" @click="refresh">{{refreshing?'正在读取…':'刷新本地状态'}}</button></div>
 <div v-if="checked.length" class="history-batch-actions"><span>已选 {{selectedRecords.length}} 份 · 每次最多 100 份</span><button class="danger-button" :disabled="busy||!cleanable.length||cleanable.length>100" @click="clean">清理对应原文件（{{cleanable.length}}）</button><button class="danger-button" :disabled="busy||!selected.length||selected.length>100" @click="confirming=true">删除本地备份包（{{selected.length}}）</button></div>
 <FeedbackNotice v-if="error" :feedback="errorFeedback(error)"/><p v-if="!rows.length" class="ready-note">暂无备份记录。创建或加载备份后，会在这里登记。</p>
 <div v-else class="history-table-wrap"><table class="history-table"><thead><tr><th><input type="checkbox" aria-label="选择最多一百份备份记录" :disabled="busy" :checked="allChecked" :indeterminate="partiallyChecked" @change="toggleAll"/></th><th>备份范围</th><th>文件状态</th><th>操作</th></tr></thead><tbody><tr v-for="row in rows" :key="row.id"><td><input type="checkbox" :value="row.id" v-model="checked" :disabled="busy" :aria-label="`选择备份 ${row.name}`"/></td><td><strong>{{row.name}}</strong><p>{{scope(row)}}</p><p><small>{{row.format==='zip'?'ZIP':'文件夹'}} · {{row.files}} 个文件<br/>{{bytes(row.bytes)}}</small></p><p><small>{{new Date(row.created_at).toLocaleString()}}</small></p><details class="history-help"><summary>保存位置</summary><p class="path-text"><button type="button" class="text-button history-location" :disabled="busy||!!opening" :title="row.format==='zip'?'在文件资源管理器中打开 ZIP 所在文件夹':'在文件资源管理器中打开此备份文件夹'" @click="openLocation(row)">{{row.path}}</button></p></details></td>
 <td class="backup-status-cell">
  <div class="presence-heading"><strong>文件存在</strong>
   <button class="text-button presence-action" type="button" title="刷新文件状态" aria-label="刷新文件状态" :disabled="busy||!!rowAction" @click="inspect(row)">↻</button>
   <button class="text-button presence-action" type="button" title="手动标注" aria-label="手动标注" :disabled="busy||!!rowAction" @click="edit(row)">✎</button>
  </div>
  <div v-for="key in presenceKeys" :key="key" class="presence-line">
   <span>{{presenceLabels[key]}}</span>
   <HelpTip :circle="false" :symbol="backupPresence(row,key).symbol" :tone="backupPresence(row,key).tone" :label="presenceLabels[key]+' '+backupPresence(row,key).description" :text="backupPresence(row,key).description"/>
  </div>
  <small v-if="row.status==='delete_failed'" class="status-badge error">备份包删除未完成</small>
 </td>
 <td><div class="history-row-actions">
  <button class="secondary" :disabled="busy||row.status==='deleting'||row.local_state!=='present'" @click="emit('load',row.id)">加载此备份</button>
  <button class="secondary" :disabled="busy||row.local_state!=='present'||row.status==='deleted'" @click="upload=row">上传至123云盘</button>
  <details class="row-more"><summary>更多操作</summary><div class="more-actions"><button v-if="row.has_loaded" class="text-button" :disabled="busy" @click="emit('view',row.id)">查看已加载媒体</button><button class="text-button" :disabled="busy" @click="emit('relocate',row.id)">重新定位</button><button v-if="row.source_cleanup_job" class="text-button" :disabled="busy" @click="emit('cleanResults',row.source_cleanup_job)">查看原文件清理结果</button><button class="danger-button" :disabled="busy||!row.files" @click="emit('clean',[row.id])">清理对应原文件</button><button class="text-button" :disabled="busy||!canForget(row)" :title="canForget(row)?'删除此条记录':'先删除本地备份包，并确认登记位置已不存在后可用'" @click="forgetting=row">删除此条记录</button></div></details>
  <button class="text-button" @click="emit('operations',row.id)">操作记录</button>
 </div></td></tr></tbody></table></div>
 <BackupCloudUpload v-if="upload" :row="upload" @close="upload=null" @updated="refresh" @login="upload=null;emit('cloudLogin')"/>
 <div v-if="editing" class="modal-backdrop" @click.self="!rowAction&&(editing=null)">
  <form class="backup-modal backup-form" role="dialog" aria-modal="true" aria-label="手动标注文件存在" @submit.prevent="saveMarks" @keydown.esc.stop="!rowAction&&(editing=null)">
   <h2>手动标注</h2>
   <label v-for="key in presenceKeys" :key="key">{{presenceLabels[key]}}
    <select v-model="markDraft[key]"><option :value="undefined">保持当前状态</option><option value="present">√ 存在</option><option value="missing">× 不存在</option></select>
   </label>
   <div class="modal-actions"><button type="button" class="secondary" :disabled="!!rowAction" @click="editing=null">取消</button><button type="submit" class="primary" :disabled="!!rowAction||!Object.values(markDraft).some(Boolean)">保存</button></div>
  </form>
 </div>
 <div v-if="confirming" class="modal-backdrop" @click.self="confirming=false"><section class="backup-modal" role="dialog" aria-modal="true" aria-label="确认批量删除本地备份包"><h2>永久删除 {{selected.length}} 份本地备份包？</h2><p>所选记录原登记大小 {{bytes(selectedBytes)}}，不代表当前实际大小。登记位置已不存在的项目只确认本地状态，不再执行磁盘删除；位置可能已改名或移动，程序不搜索其他目录。仍存在的包会在执行前重新校验。此操作不清理原文件或独立副本，不经过回收站；请确认仍保有需要的备份。历史清单继续保留，已清理且未重新出现的位置不重复处理；历史和原文件关联继续保留。</p><ul class="delete-review"><li v-for="row in selected" :key="row.id">{{row.name}}<span class="path-text">{{row.path}}</span></li></ul><div class="modal-actions"><button class="secondary" @click="confirming=false">取消</button><button class="danger-button" :disabled="props.busy" @click="remove">永久删除这些本地备份包</button></div></section></div>
 <div v-if="forgetting" class="modal-backdrop" @click.self="forgetting=null"><section class="backup-modal" role="dialog" aria-modal="true" aria-label="确认删除此条记录"><h2>删除此条记录？</h2><p><strong>{{forgetting.name}} · {{scope(forgetting)}}</strong></p><p class="path-text">{{forgetting.path}}</p><p>将移除这条备份记录、保存的备份清单及其原文件关联，之后无法从此记录“清理对应原文件”或重新定位。关联但尚未执行的原文件清理确认会失效，需要重新预检。</p><p>原文件、已加载的媒体副本及独立保存的加载记录、原文件清理结果会保留。此操作无法撤销；如果仍保有完整备份包，可通过“加载备份”重新登记。</p><div class="modal-actions"><button class="secondary" :disabled="busy" @click="forgetting=null">取消</button><button class="danger-button" :disabled="busy" @click="forget">删除此条记录</button></div></section></div>
</section>
</template>
