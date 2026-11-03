<script setup lang="ts">
import FeedbackNotice from './FeedbackNotice.vue'
import {errorFeedback} from '../feedback'
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {api,type BackupRecord} from '../api'
import HelpTip from './HelpTip.vue'
import {packagePresence,originalPresence,cleanupLabel,cleanupCounts,restoreLabel,restoreTimeText,fmtTime} from '../backupPresentation'
const props=defineProps<{busy:boolean}>()
const emit=defineEmits<{load:[id:string];relocate:[id:string];view:[id:string];remove:[ids:string[]];forget:[id:string];check:[id:string];clean:[ids:string[]];cleanResults:[jobID:string]}>()
const refreshing=ref(false)
const opening=ref('')
const rows=ref<BackupRecord[]>([]),checked=ref<string[]>([]),error=ref(''),confirming=ref(false)
const forgetting=ref<BackupRecord|null>(null)
const canForget=(row:BackupRecord)=>row.status==='deleted'&&row.local_state==='missing'
const selectedRecords=computed(()=>rows.value.filter(r=>checked.value.includes(r.id)))
const selected=computed(()=>selectedRecords.value.filter(r=>r.status!=='deleted'||r.local_state==='present'||r.local_state==='pending'))
const cleanable=computed(()=>selectedRecords.value.filter(r=>r.files>0))
const allChecked=computed(()=>rows.value.slice(0,100).length>0&&rows.value.slice(0,100).every(r=>checked.value.includes(r.id)))
const partiallyChecked=computed(()=>checked.value.length>0&&!allChecked.value)
const selectedBytes=computed(()=>selected.value.reduce((sum:number,row:BackupRecord)=>sum+row.bytes,0))
const labels:Record<string,string>={available:'本地位置已登记',deleted:'登记位置已确认清理',deleting:'本地包删除中 / 待继续',delete_failed:'本地包删除未完成',unavailable:'本地位置不可用'}
function tone(state?:string){return ['completed','qman_only','present'].includes(state??'')?'success':['failed','changed'].includes(state??'')?'error':['unknown','partial','fallback','interrupted','cancelled','pending','delete_failed'].includes(state??'')?'warning':'muted'}
const bytes=(v:number)=>`${(v/1024/1024).toLocaleString(undefined,{maximumFractionDigits:1})} MiB`
function scope(r:BackupRecord){const f=r.selection;const names:Record<string,string>={image:'图片',video:'视频',emoji:'收到的表情',voice:'语音'};return `${names[f.kind]||'全部类型'} · ${f.month_from||f.month_to?`${f.month_from||'不限'} 至 ${f.month_to||'不限'}`:f.month||'全部月份'}${f.view==='observed'?' · 发现日期视图':''}${f.from||f.to?` · ${f.from||'不限'} 至 ${f.to||'不限'}`:''}`}
let loading=false,disposed=false
async function refresh(){if(loading||disposed)return;loading=true;refreshing.value=true;try{const result=await api().GetBackupHistory();if(!disposed){rows.value=result;error.value=''}}catch(e){if(!disposed)error.value=String(e)}finally{loading=false;refreshing.value=false}}
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
 <div class="history-toolbar"><p>查看备份位置、原文件状态与操作结果。</p><button class="secondary" :disabled="busy||refreshing" @click="refresh">{{refreshing?'正在检查位置…':'刷新本地状态'}}</button></div>
 <div v-if="checked.length" class="history-batch-actions"><span>已选 {{selectedRecords.length}} 份 · 每次最多 100 份</span><button class="danger-button" :disabled="busy||!cleanable.length||cleanable.length>100" @click="clean">清理对应原文件（{{cleanable.length}}）</button><button class="danger-button" :disabled="busy||!selected.length||selected.length>100" @click="confirming=true">删除本地备份包（{{selected.length}}）</button></div>
 <FeedbackNotice v-if="error" :feedback="errorFeedback(error)"/><p v-if="!rows.length" class="ready-note">暂无备份记录。创建或加载备份后，会在这里登记。</p>
 <div v-else class="history-table-wrap"><table class="history-table"><thead><tr><th><input type="checkbox" aria-label="选择最多一百份备份记录" :disabled="busy" :checked="allChecked" :indeterminate="partiallyChecked" @change="toggleAll"/></th><th>备份范围</th><th>状态与结果</th><th>操作</th></tr></thead><tbody><tr v-for="row in rows" :key="row.id"><td><input type="checkbox" :value="row.id" v-model="checked" :disabled="busy" :aria-label="`选择备份 ${row.name}`"/></td><td><strong>{{row.name}}</strong><p>{{scope(row)}}</p><p><small>{{row.format==='zip'?'ZIP':'文件夹'}} · {{row.files}} 个文件<br/>{{bytes(row.bytes)}} · 创建时登记</small></p><p><small>{{new Date(row.created_at).toLocaleString()}}</small></p><details class="history-help"><summary>保存位置</summary><p class="path-text"><button type="button" class="text-button history-location" :disabled="busy||!!opening" :title="row.format==='zip'?'在文件资源管理器中打开 ZIP 所在文件夹':'在文件资源管理器中打开此备份文件夹'" @click="openLocation(row)">{{row.path}}</button></p></details></td>
 <td class="backup-status-cell"><div class="backup-status-grid">
  <div class="backup-status-block presence-group"><strong>文件是否存在</strong><div class="presence-line"><span>备份包：</span><HelpTip :circle="false" :symbol="packagePresence(row).symbol" :tone="packagePresence(row).tone" :label="'备份包：'+packagePresence(row).description" :text="packagePresence(row).description"/></div><div class="presence-line"><span>本地源文件：</span><HelpTip :circle="false" :symbol="originalPresence(row).symbol" :tone="originalPresence(row).tone" :label="'本地源文件：'+originalPresence(row).description" :text="originalPresence(row).description"/></div><small v-if="row.status==='delete_failed'" class="status-badge error">备份包删除未完成</small></div>
  <div class="backup-status-block"><strong>源文件回收</strong><span class="status-badge" :class="tone(row.cleanup_result?.state==='completed'&&(row.cleanup_result.skipped||row.cleanup_result.failed||row.cleanup_result.uncertain||row.cleanup_result.remaining)?'partial':row.cleanup_result?.state)">{{cleanupLabel(row)}}</span><small v-if="cleanupCounts(row)">{{row.cleanup_result?.joint?'联合任务 · ':''}}{{cleanupCounts(row)}}</small></div>
  <div class="backup-status-block"><strong>最近加载</strong><span class="status-badge" :class="tone(row.restore_result?.state)">{{restoreLabel(row)}}</span><small v-if="restoreTimeText(row)">{{restoreTimeText(row)}}</small></div>
 </div>
 <details><summary>核对时间与操作详情</summary>
  <p v-if="row.location_checked_at">备份位置检查：{{fmtTime(row.location_checked_at)}}<br/>{{row.local_detail}}</p>
  <p v-if="row.original_files">原文件核对：{{fmtTime(row.original_files.checked_at)}}<br/>存在 {{row.original_files.present}} · 未找到 {{row.original_files.missing}} · 大小/类型变化 {{row.original_files.changed}} · 未知 {{row.original_files.unknown}}<br/>仅路径与大小检查，不校验内容。</p>
  <p v-if="row.cleanup_result">原文件清理{{row.cleanup_result.joint?'（联合任务）':''}}：{{fmtTime(row.cleanup_result.at)}}<br/>回收 {{row.cleanup_result.recycled}} · 跳过 {{row.cleanup_result.skipped}} · 失败 {{row.cleanup_result.failed}} · 待核对 {{row.cleanup_result.uncertain}} · 尚未执行 {{row.cleanup_result.remaining}}</p>
  <p v-if="row.restore_result">加载开始：{{fmtTime(row.restore_result.started_at)}}<br/>加载结束：{{fmtTime(row.restore_result.finished_at)}}<template v-if="row.restore_result.state!=='legacy'"><br/>补回 {{row.restore_result.written}} · 相同跳过 {{row.restore_result.skipped}} · 冲突另存 {{row.restore_result.conflicts}} · 仅 Qman {{row.restore_result.fallback}} · 未完成 {{row.restore_result.failed}}</template><span v-if="row.restore_result.target" class="path-text">请求目标：{{row.restore_result.target}}</span><br v-if="row.restore_result.detail"/>{{row.restore_result.detail}}</p>
  <p>{{labels[row.status]||row.status}} · {{row.detail}}</p><p v-if="row.source_cleanup_detail&&!row.cleanup_result">{{row.source_cleanup_detail}}</p>
 </details></td>
 <td><div class="history-row-actions"><button class="secondary" :disabled="busy||row.status==='deleting'||row.local_state==='missing'||row.local_state==='unavailable'||row.local_state==='pending'" @click="emit('load',row.id)">加载备份</button><button class="text-button" :disabled="busy" @click="emit('check',row.id)">核对原文件</button>
 <details class="row-more"><summary>更多操作</summary><div class="more-actions"><button class="text-button" :disabled="busy" @click="emit('view',row.id)" title="浏览此前加载后的文件，不会再次复制；预览仍需文件可读">查看已加载媒体</button><button class="text-button" :disabled="busy" @click="emit('relocate',row.id)">重新定位</button><button v-if="row.source_cleanup_job" class="text-button" :disabled="busy" @click="emit('cleanResults',row.source_cleanup_job)">查看原文件清理结果</button><button class="danger-button" :disabled="busy||!row.files" @click="emit('clean',[row.id])">清理对应原文件</button><button class="text-button" :disabled="busy||!canForget(row)" :title="canForget(row)?'删除此条记录':'先删除本地备份包，并确认登记位置已不存在后可用'" @click="forgetting=row">删除此条记录</button></div></details>
 </div></td></tr></tbody></table></div>
 <div v-if="confirming" class="modal-backdrop" @click.self="confirming=false"><section class="backup-modal" role="dialog" aria-modal="true" aria-label="确认批量删除本地备份包"><h2>永久删除 {{selected.length}} 份本地备份包？</h2><p>所选记录原登记大小 {{bytes(selectedBytes)}}，不代表当前实际大小。登记位置已不存在的项目只确认本地状态，不再执行磁盘删除；位置可能已改名或移动，程序不搜索其他目录。仍存在的包会在执行前重新校验。此操作不清理原文件或独立副本，不经过回收站；请确认仍保有需要的备份。历史清单继续保留，已清理且未重新出现的位置不重复处理；历史和原文件关联继续保留。</p><ul class="delete-review"><li v-for="row in selected" :key="row.id">{{row.name}}<span class="path-text">{{row.path}}</span></li></ul><div class="modal-actions"><button class="secondary" @click="confirming=false">取消</button><button class="danger-button" :disabled="props.busy" @click="remove">永久删除这些本地备份包</button></div></section></div>
 <div v-if="forgetting" class="modal-backdrop" @click.self="forgetting=null"><section class="backup-modal" role="dialog" aria-modal="true" aria-label="确认删除此条记录"><h2>删除此条记录？</h2><p><strong>{{forgetting.name}} · {{scope(forgetting)}}</strong></p><p class="path-text">{{forgetting.path}}</p><p>将移除这条备份记录、保存的备份清单及其原文件关联，之后无法从此记录“清理对应原文件”或重新定位。关联但尚未执行的原文件清理确认会失效，需要重新预检。</p><p>原文件、已加载的媒体副本及独立保存的加载记录、原文件清理结果会保留。此操作无法撤销；如果仍保有完整备份包，可通过“加载备份”重新登记。</p><div class="modal-actions"><button class="secondary" :disabled="busy" @click="forgetting=null">取消</button><button class="danger-button" :disabled="busy" @click="forget">删除此条记录</button></div></section></div>
</section>
</template>
