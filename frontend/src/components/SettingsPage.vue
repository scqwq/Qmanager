<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {api,pathSettingsChanged,type PathSettings} from '../api'
import HelpTip from './HelpTip.vue'

const props=defineProps<{busy:boolean;recordsMessage?:string;recordsError?:string}>()
const emit=defineEmits<{imported:[];exportRecords:[]}>()
const saved=ref<PathSettings>({source_directory:'',backup_directory:''})
const source=ref(''),backup=ref(''),error=ref(''),message=ref('')
const loading=ref(true),saving=ref(false),choosing=ref(false),importing=ref(false),loaded=ref(false)
const pending=computed(()=>loading.value||saving.value||choosing.value||importing.value)
const disabled=computed(()=>props.busy||pending.value||!loaded.value)
const dirty=computed(()=>source.value.trim()!==saved.value.source_directory||backup.value.trim()!==saved.value.backup_directory)
let disposed=false
function reset(){source.value=saved.value.source_directory;backup.value=saved.value.backup_directory;error.value='';message.value=''}
async function load(){loading.value=true;error.value='';try{const value=await api().GetSettings();if(!disposed){saved.value=value;reset();loaded.value=true}}catch(e){if(!disposed)error.value=String(e)}finally{loading.value=false}}
async function choose(kind:'source'|'backup'){
 choosing.value=true;error.value='';message.value=''
 try{const path=await api().ChooseSettingsDirectory(kind,kind==='source'?source.value:backup.value);if(path&&!disposed){if(kind==='source')source.value=path;else backup.value=path}}
 catch(e){if(!disposed)error.value=String(e)}finally{choosing.value=false}
}
async function save(){
 saving.value=true;error.value='';message.value=''
 try{const value=await api().SaveSettings({source_directory:source.value.trim(),backup_directory:backup.value.trim()});if(!disposed){saved.value=value;reset();message.value='默认路径已保存，下次打开仍会保留。'}}
 catch(e){if(!disposed)error.value=String(e)}finally{saving.value=false}
}
async function importSource(){
 importing.value=true;error.value='';message.value=''
 try{await api().ImportDefaultSource();if(!disposed)emit('imported')}
 catch(e){if(!disposed)error.value=String(e)}finally{importing.value=false}
}
function refreshSyncedSettings(){if(!dirty.value&&!pending.value)void load()}
onMounted(()=>{void load();window.addEventListener(pathSettingsChanged,refreshSyncedSettings)})
onUnmounted(()=>{disposed=true;window.removeEventListener(pathSettingsChanged,refreshSyncedSettings)})
</script>

<template>
<section class="settings-page">
 <p class="settings-intro">默认目录</p>
 <p v-if="loading" role="status">正在读取设置…</p>
 <p v-if="error" class="notice error" role="alert">{{error}}<button v-if="!loaded" class="text-button" :disabled="loading" @click="load">重新读取</button></p>
 <p v-if="message" class="notice success" role="status">{{message}}</p>
 <form class="settings-form" @submit.prevent="save">
  <section class="settings-field">
   <label for="default-source">默认 QQ 数据文件夹</label>
   <p id="source-help">用于导入媒体和填入备份恢复目标。支持 Tencent Files、账号目录或 nt_data。</p>
   <div class="settings-path"><input id="default-source" v-model="source" @input="message=''" :disabled="disabled" placeholder="未设置，导入时手动选择" aria-describedby="source-help" spellcheck="false"/><button type="button" class="secondary" :disabled="disabled" @click="choose('source')">选择文件夹</button><button type="button" class="text-button" :disabled="disabled||!source" @click="source='';message=''">清除</button></div>

   <button type="button" class="secondary" :disabled="disabled||dirty||!saved.source_directory" @click="importSource">{{importing?'正在导入…':'导入默认目录'}}</button><small v-if="dirty">请先保存修改，再导入。</small><details class="settings-help"><summary>目录如何使用？</summary><p>导入默认目录只识别账号，更新需手动开始。未设置时，首次导入或选择恢复目标会填入此设置，已有默认值不被临时选择覆盖。</p></details>
  </section>
  <section class="settings-field">
   <label for="default-backup">默认备份保存目录</label>
   <p id="backup-help">备份时优先使用，可为单次备份另选位置。请与 QQ 数据目录分开。</p>
   <div class="settings-path"><input id="default-backup" v-model="backup" @input="message=''" :disabled="disabled" placeholder="未设置，沿用上次选择的保存目录" aria-describedby="backup-help" spellcheck="false"/><button type="button" class="secondary" :disabled="disabled" @click="choose('backup')">选择文件夹</button><button type="button" class="text-button" :disabled="disabled||!backup" @click="backup='';message=''">清除</button></div>
   <details class="settings-help"><summary>未设置时如何保存？</summary><p>沿用上次保存位置；首次选择会填入默认目录。单次选择不会覆盖已有默认值。</p></details>
  </section>
  <div class="settings-actions"><button type="submit" class="primary" :disabled="disabled||!dirty">{{saving?'正在保存…':'保存设置'}}</button><button type="button" class="secondary" :disabled="disabled||!dirty" @click="reset">撤销修改</button><small>保存只记录路径，不会导入或扫描。</small></div>
 </form>
 <section class="settings-records" aria-label="应用记录">
  <div class="records-actions"><button type="button" class="secondary" :disabled="disabled" @click="emit('exportRecords')">备份应用记录</button><HelpTip label="关于备份应用记录" text="导出 Qman 保存的媒体清单、观察历史和操作记录的数据和缓存，不包含图片、视频等原文件。目前仅支持导出，缓存指索引查询数据。"/></div>
  <p v-if="recordsError" class="notice error" role="alert">{{recordsError}}</p><p v-if="recordsMessage" class="notice success" role="status">{{recordsMessage}}</p>
 </section>
</section>
</template>
