<script setup lang="ts">
import FeedbackNotice from './FeedbackNotice.vue'
import {errorFeedback} from '../feedback'
import {computed,nextTick,onMounted,onUnmounted,ref} from 'vue'
import {api,pathSettingsChanged,type PathSettings} from '../api'
import HelpTip from './HelpTip.vue'
import AppearanceSettings from './AppearanceSettings.vue'
import LogsPage from './LogsPage.vue'
import CloudPage from './CloudPage.vue'

const props=defineProps<{busy:boolean;recordsMessage?:string;recordsError?:string}>()
const emit=defineEmits<{imported:[];exportRecords:[]}>()
const saved=ref<PathSettings>({source_directory:'',backup_directory:''})
const source=ref(''),backup=ref(''),error=ref(''),message=ref('')
const loading=ref(true),saving=ref(false),choosing=ref(false),importing=ref(false),loaded=ref(false)
const logsOpen=ref(false)
const cloudOpen=ref(false)
const searching=ref(false),cancellingSearch=ref(false),searchHint=ref(''),searchDetail=ref(''),searchCandidates=ref<string[]>([])
let searchID='',searchTimer:ReturnType<typeof setTimeout>|undefined
let settingsScroll=0
async function openLogs(){settingsScroll=window.scrollY;logsOpen.value=true;await nextTick();window.scrollTo(0,0)}
async function closeLogs(){logsOpen.value=false;await nextTick();window.scrollTo(0,settingsScroll)}
async function openCloud(){settingsScroll=window.scrollY;cloudOpen.value=true;await nextTick();window.scrollTo(0,0)}
async function closeCloud(){cloudOpen.value=false;await nextTick();window.scrollTo(0,settingsScroll)}
const pending=computed(()=>loading.value||saving.value||choosing.value||importing.value||searching.value)
const disabled=computed(()=>props.busy||pending.value||!loaded.value)
const dirty=computed(()=>source.value.trim()!==saved.value.source_directory||backup.value.trim()!==saved.value.backup_directory)
let disposed=false
function reset(){source.value=saved.value.source_directory;backup.value=saved.value.backup_directory;error.value='';message.value='';searchHint.value='';searchCandidates.value=[]}
async function load(){loading.value=true;error.value='';try{const value=await api().GetSettings();if(!disposed){saved.value=value;reset();loaded.value=true}}catch(e){if(!disposed)error.value=String(e)}finally{loading.value=false}}
async function choose(kind:'source'|'backup'){
 choosing.value=true;error.value='';message.value=''
 try{const path=await api().ChooseSettingsDirectory(kind,kind==='source'?source.value:backup.value);if(path&&!disposed){if(kind==='source')source.value=path;else backup.value=path}}
 catch(e){if(!disposed)error.value=String(e)}finally{choosing.value=false}
}
async function save(){
 saving.value=true;error.value='';message.value=''
 try{const value=await api().SaveSettings({source_directory:source.value.trim(),backup_directory:backup.value.trim()});if(!disposed){saved.value=value;reset();message.value='默认目录已保存。'}}
 catch(e){if(!disposed)error.value=String(e)}finally{saving.value=false}
}
async function importSource(){
 importing.value=true;error.value='';message.value=''
 try{await api().ImportDefaultSource();if(!disposed)emit('imported')}
 catch(e){if(!disposed)error.value=String(e)}finally{importing.value=false}
}
function finishSearch(id:string){if(searchID!==id)return;searchID='';searching.value=false;cancellingSearch.value=false;if(searchTimer)clearTimeout(searchTimer);searchTimer=undefined}
async function searchSource(){
 if(searching.value){
  const id=searchID;cancellingSearch.value=true
  try{await api().CancelQQDirectorySearch(id);if(!disposed&&searchID===id){searchHint.value='搜索已取消。';finishSearch(id)}}
  catch{if(!disposed&&searchID===id){cancellingSearch.value=false;searchHint.value='取消未完成，请重试。'}}
  return
 }
 if(disabled.value)return
 const id=Date.now().toString(36)+'-'+Math.random().toString(36).slice(2)
 searchID=id;searching.value=true;searchHint.value='';searchDetail.value='';searchCandidates.value=[];message.value=''
 searchTimer=setTimeout(()=>{if(!disposed&&searchID===id){searchHint.value='搜索已超时，请选择文件夹。';finishSearch(id);void api().CancelQQDirectorySearch(id).catch(()=>{})}},30000)
 try{
  const result=await api().SearchQQDirectory(id,source.value.trim(),backup.value.trim())
  if(disposed||searchID!==id||cancellingSearch.value)return
  if(result.status==='found'&&result.candidates.length){
   searchCandidates.value=result.candidates
   if(result.candidates.length===1){source.value=result.candidates[0];if(!result.complete)searchHint.value='已填入找到的位置，部分目录未检查。'}
   else{searchHint.value=`找到 ${result.candidates.length} 个位置，可在目录框选择。`;finishSearch(id);await nextTick();if(disposed)return;const input=document.getElementById('default-source') as HTMLInputElement|null;input?.focus();input?.select()}
  }else if(result.status==='cancelled')searchHint.value='搜索已取消。'
  else if(result.status==='timeout')searchHint.value='搜索已超时，请选择文件夹。'
  else searchHint.value=result.complete?'未找到，请选择文件夹。':'部分目录未检查，请选择文件夹。'
 }catch(e){if(!disposed&&searchID===id){searchDetail.value=String(e);searchHint.value=searchDetail.value.includes('仍在停止')?'上次搜索仍在停止，请稍后重试。':searchDetail.value.includes('当前任务')?'请等待当前任务结束后搜索。':'搜索未完成，请稍后重试或选择文件夹。'}}
 finally{if(!cancellingSearch.value)finishSearch(id)}
}
function refreshSyncedSettings(){if(!dirty.value&&!pending.value)void load()}
onMounted(()=>{void load();window.addEventListener(pathSettingsChanged,refreshSyncedSettings)})
onUnmounted(()=>{disposed=true;const id=searchID;finishSearch(id);if(id)void api().CancelQQDirectorySearch(id).catch(()=>{});window.removeEventListener(pathSettingsChanged,refreshSyncedSettings)})
</script>

<template>
<section class="settings-page">
 <LogsPage v-if="logsOpen" @back="closeLogs"/>
 <CloudPage v-if="cloudOpen" @back="closeCloud"/>
 <div v-show="!logsOpen&&!cloudOpen">
 <AppearanceSettings/>
 <p class="settings-intro">默认目录</p>
 <p v-if="loading" role="status">正在读取设置…</p>
 <div v-if="error"><FeedbackNotice :feedback="errorFeedback(error)"/><button v-if="!loaded" class="text-button" :disabled="loading" @click="load">重新读取设置</button></div>
 <p v-if="message" class="notice success" role="status">{{message}}</p>
 <form class="settings-form" @submit.prevent="save">
  <section class="settings-field">
   <label for="default-source">默认 QQ 数据文件夹</label>
   <p id="source-help">最好是Tencent Files，账号目录或 nt_data也可</p>
   <div class="settings-path"><input id="default-source" v-model="source" :list="searchCandidates.length?'qq-search-candidates':undefined" @input="message='';searchHint=''" :disabled="disabled" placeholder="未设置，导入时手动选择" aria-describedby="source-help" spellcheck="false"/><datalist id="qq-search-candidates"><option v-for="path in searchCandidates" :key="path" :value="path"/></datalist><button type="button" class="secondary" :disabled="disabled" @click="choose('source')">选择文件夹</button><button type="button" class="text-button" :disabled="disabled||!source" @click="source='';message='';searchHint=''">清除</button></div>

   <div class="settings-import-actions"><button type="button" class="secondary" :disabled="disabled||dirty||!saved.source_directory" @click="importSource">{{importing?'正在导入…':'导入默认目录'}}</button><button type="button" class="secondary" :disabled="searching?cancellingSearch:disabled" @click="searchSource">{{searching?'取消搜索':'搜索 QQ 目录'}}</button><small v-if="searchHint" class="directory-search-hint" :title="searchDetail||undefined" role="status" aria-live="polite">{{searchHint}}</small></div><small v-if="dirty">请先保存修改，再导入。</small><details class="settings-help"><summary>目录如何使用？</summary><p>导入默认目录只识别账号，更新需手动开始。未设置时，首次导入或选择恢复目标会填入此设置，已有默认值不被临时选择覆盖。</p></details>
  </section>
  <section class="settings-field">
   <label for="default-backup">默认备份保存目录</label>
   <div class="settings-path"><input id="default-backup" v-model="backup" @input="message=''" :disabled="disabled" placeholder="未设置，沿用上次选择的保存目录" spellcheck="false"/><button type="button" class="secondary" :disabled="disabled" @click="choose('backup')">选择文件夹</button><button type="button" class="text-button" :disabled="disabled||!backup" @click="backup='';message=''">清除</button></div>
   <details class="settings-help"><summary>未设置时如何保存？</summary><p>沿用上次保存位置；首次选择会填入默认目录。单次选择不会覆盖已有默认值。</p></details>
  </section>
  <div class="settings-actions"><button type="submit" class="primary" :disabled="disabled||!dirty">{{saving?'正在保存…':'保存设置'}}</button><button type="button" class="secondary" :disabled="disabled||!dirty" @click="reset">撤销修改</button><small>保存只记录路径，不会导入或扫描。</small></div>
 </form>
 <section class="settings-records" aria-label="应用记录">
  <div class="records-actions"><button type="button" class="secondary" :disabled="disabled" @click="emit('exportRecords')">备份应用记录</button><HelpTip label="关于备份应用记录" text="导出 Qman 保存的媒体清单、观察历史和操作记录的数据和缓存，不包含图片、视频等原文件。目前仅支持导出，缓存指索引查询数据。"/></div>
  <FeedbackNotice v-if="recordsError" :feedback="errorFeedback(recordsError)"/><p v-if="recordsMessage" class="notice success" role="status">{{recordsMessage}}</p>
 </section>
 <section class="settings-log-entry">
  <div><h2>123云盘管理</h2></div>
  <button type="button" class="secondary" @click="openCloud">云盘登录</button>
 </section>
 <section class="settings-log-entry">
  <div><h2>运行日志</h2><p>查看回收过程和崩溃记录，排查操作中断。</p></div>
  <button type="button" class="secondary" @click="openLogs">查看日志</button>
 </section>
 </div>
</section>
</template>
