<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {api,pathSettingsChanged,type PathSettings} from '../api'

const props=defineProps<{busy:boolean}>()
const emit=defineEmits<{imported:[]}>()
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
 <div class="page-heading"><div><h1>设置</h1><p>设置常用目录，减少每次选择路径的步骤。</p></div></div>
 <p v-if="loading" role="status">正在读取设置…</p>
 <p v-if="error" class="notice error" role="alert">{{error}}<button v-if="!loaded" class="text-button" :disabled="loading" @click="load">重新读取</button></p>
 <p v-if="message" class="notice success" role="status">{{message}}</p>
 <form class="settings-form" @submit.prevent="save">
  <section class="settings-field">
   <label for="default-source">默认 QQ 数据文件夹</label>
   <p id="source-help">可选择最外层 Tencent Files、迁移父目录、账号目录或 nt_data。导入时的目录窗口会从这里打开，加载备份也会据此填入目标 Tencent Files。</p>
   <div class="settings-path"><input id="default-source" v-model="source" @input="message=''" :disabled="disabled" placeholder="未设置，导入时手动选择" aria-describedby="source-help" spellcheck="false"/><button type="button" class="secondary" :disabled="disabled" @click="choose('source')">选择 QQ 数据文件夹</button><button type="button" class="text-button" :disabled="disabled||!source" @click="source='';message=''">清除</button></div>
   <p>未设置默认目录时，首次导入或加载备份中选择的 QQ 目录会自动同步到这里。保存只记录目录。点击下方“导入默认目录”后识别账号，再手动开始快速更新。</p>
   <button type="button" class="secondary" :disabled="disabled||dirty||!saved.source_directory" @click="importSource">{{importing?'正在导入…':'导入默认目录'}}</button><small v-if="dirty">请先保存修改，再导入默认目录。</small>
  </section>
  <section class="settings-field">
   <label for="default-backup">默认备份保存目录</label>
   <p id="backup-help">备份弹窗优先填入此目录，可为单次备份另选位置。保存目录须与 QQ 数据文件夹分开。</p>
   <div class="settings-path"><input id="default-backup" v-model="backup" @input="message=''" :disabled="disabled" placeholder="未设置，沿用上次选择的保存目录" aria-describedby="backup-help" spellcheck="false"/><button type="button" class="secondary" :disabled="disabled" @click="choose('backup')">选择保存目录</button><button type="button" class="text-button" :disabled="disabled||!backup" @click="backup='';message=''">清除</button></div>
   <p>未设置时，首次选择备份保存目录会自动同步到这里；已有默认值不被单次选择覆盖。</p>
  </section>
  <div class="settings-actions"><button type="submit" class="primary" :disabled="disabled||!dirty">{{saving?'正在保存…':'保存默认路径'}}</button><button type="button" class="secondary" :disabled="disabled||!dirty" @click="reset">撤销修改</button></div>
 </form>
</section>
</template>
