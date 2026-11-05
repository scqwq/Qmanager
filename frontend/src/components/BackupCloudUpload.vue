<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {api,type BackupRecord,type CloudFolder,type CloudStatus} from '../api'
import CloudFolderPicker from './CloudFolderPicker.vue'
import FeedbackNotice from './FeedbackNotice.vue'
import {cloudErrorFeedback} from '../cloudFeedback'

const props=defineProps<{row:BackupRecord}>()
const emit=defineEmits<{close:[];updated:[]}>()
const trail=ref<CloudFolder[]>([{id:'0',name:'我的文件'}])
const targetPath=computed(()=>trail.value.map(folder=>folder.name).join(' / '))
const status=ref<CloudStatus|null>(null)
const picker=ref(false),working=ref(false),started=ref(false),error=ref('')
const activePhases=['checking','initializing','uploading','completing','verifying']
const active=computed(()=>activePhases.includes(status.value?.upload.phase??''))
const task=computed(()=>started.value?status.value?.upload:null)
const labels:Record<string,string>={checking:'核对并准备备份',initializing:'准备上传',uploading:'上传中',completing:'完成登记',verifying:'核对结果',complete:'上传完成',failed:'上传未完成',cancelled:'已取消'}
let timer:ReturnType<typeof setTimeout>|undefined,disposed=false

async function update(){
 try{
  const next=await api().GetCloudStatus()
  if(disposed)return
  const wasActive=active.value
  status.value=next
  if(active.value)timer=setTimeout(()=>void update(),800)
  else if(started.value&&(wasActive||working.value))emit('updated')
 }catch(e){if(!disposed)error.value=String(e)}
}
async function open(){
 working.value=true
 try{
  const next=await api().GetCloudStatus()
  status.value=next.needs_check?await api().RefreshCloudLogin():next
  if(active.value)timer=setTimeout(()=>void update(),800)
 }catch(e){if(!disposed)error.value=String(e)}
 finally{working.value=false}
}
async function start(){
 if(working.value||active.value||!status.value?.logged_in)return
 error.value=''
 working.value=true
 try{
  await api().UploadRegisteredBackup(props.row.id,trail.value.at(-1)!.id)
  if(disposed)return
  started.value=true
  await update()
 }catch(e){if(!disposed)error.value=String(e)}
 finally{working.value=false}
}
function close(){if(!active.value&&!working.value)emit('close')}
function selectFolder(value:CloudFolder[]){trail.value=value;picker.value=false}
onMounted(()=>void open())
onUnmounted(()=>{disposed=true;if(timer)clearTimeout(timer)})
</script>

<template>
 <div class="modal-backdrop" @click.self="close">
  <section class="backup-modal backup-form" :inert="picker" role="dialog" aria-modal="true" aria-labelledby="backup-upload-title" @keydown.esc.stop="close">
   <h2 id="backup-upload-title">上传备份到123云盘</h2>
   <p>{{row.name}} · {{row.files}} 个文件</p>
   <div v-if="status?.logged_in&&!started" class="cloud-target">
    <span class="cloud-target-path">当前操作目录：{{targetPath}}</span>
    <button type="button" class="secondary" :disabled="working||active" @click="picker=true">选择目录</button>
   </div>
   <p v-if="status&&!status.logged_in&&!started">请先在设置中登录123云盘。</p>
   <p v-if="active&&!started" role="status">已有上传正在进行。</p>
   <template v-if="task">
    <p role="status">{{labels[task.phase]??task.phase}}</p>
    <progress v-if="active" :value="task.total?task.done/task.total:undefined" max="1" aria-label="上传进度"/>
    <FeedbackNotice v-if="task.phase==='failed'" :feedback="cloudErrorFeedback(task.detail)"/>
    <p v-else-if="task.detail">{{task.detail}}</p>
   </template>
   <FeedbackNotice v-if="error" :feedback="cloudErrorFeedback(error)"/>
   <div class="modal-actions">
    <button v-if="started&&active" class="secondary" type="button" @click="api().CancelCloudUpload()">取消上传</button>
    <button v-else class="secondary" type="button" :disabled="working||active" @click="close">{{started?'关闭':'取消'}}</button>
    <button v-if="!started" class="primary" type="button" :disabled="working||active||!status?.logged_in" @click="start">开始上传</button>
   </div>
  </section>
 </div>
 <CloudFolderPicker v-if="picker" :trail="trail" @close="picker=false" @select="selectFolder" @login-required="picker=false;open()"/>
</template>
