<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {api,type BackupRecord,type CloudFolder,type CloudStatus} from '../api'
import CloudFolderPicker from './CloudFolderPicker.vue'
import FeedbackNotice from './FeedbackNotice.vue'
import {cloudErrorFeedback} from '../cloudFeedback'

const props=defineProps<{row:BackupRecord}>()
const emit=defineEmits<{close:[];updated:[];login:[]}>()
const trail=ref<CloudFolder[]>([{id:'0',name:'我的文件'}])
const targetPath=computed(()=>trail.value.map(folder=>folder.name).join(' / '))
const status=ref<CloudStatus|null>(null)
const dialog=ref<HTMLElement|null>(null)
const picker=ref(false),working=ref(false),started=ref(false),error=ref('')
const activePhases=['checking','initializing','uploading','completing','verifying']
const active=computed(()=>activePhases.includes(status.value?.upload.phase??''))
const task=computed(()=>started.value?status.value?.upload:null)
const loginRequired=computed(()=>!started.value&&status.value!==null&&!status.value.logged_in&&!status.value.needs_check)
const labels:Record<string,string>={checking:'核对并准备备份',initializing:'准备上传',uploading:'上传中',completing:'完成登记',verifying:'核对结果',complete:'上传完成',failed:'上传未完成',cancelled:'已取消'}
let timer:ReturnType<typeof setTimeout>|undefined,disposed=false
let previousFocus:HTMLElement|null=null

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
 error.value=''
 try{
  const next=await api().GetCloudStatus()
  if(disposed)return
  status.value=next
  if(next.needs_check)status.value=await api().RefreshCloudLogin()
  if(disposed)return
  if(active.value)timer=setTimeout(()=>void update(),800)
 }catch(e){await readStatusAfterFailure(e)}
 finally{working.value=false}
}
async function readStatusAfterFailure(cause:unknown){
 const latest=await api().GetCloudStatus().catch(()=>null)
 if(disposed)return
 if(latest)status.value=latest
 error.value=loginRequired.value?'':String(cause)
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
 }catch(e){await readStatusAfterFailure(e)}
 finally{working.value=false}
}
function close(){if(!(started.value&&active.value)&&!working.value)emit('close')}
function keys(event:KeyboardEvent){
 if(event.key==='Escape'){event.stopPropagation();close();return}
 if(event.key!=='Tab')return
 const buttons=dialog.value?.querySelectorAll<HTMLButtonElement>('button:not([disabled])')
 if(!buttons?.length)return
 const first=buttons[0],last=buttons[buttons.length-1]
 if(event.shiftKey&&(document.activeElement===first||document.activeElement===dialog.value)){
  event.preventDefault()
  last.focus()
 }else if(!event.shiftKey&&document.activeElement===last){
  event.preventDefault()
  first.focus()
 }
}
function selectFolder(value:CloudFolder[]){trail.value=value;picker.value=false}
onMounted(()=>{
 previousFocus=document.activeElement as HTMLElement|null
 dialog.value?.focus()
 void open()
})
onUnmounted(()=>{
 disposed=true
 if(timer)clearTimeout(timer)
 if(previousFocus?.isConnected)previousFocus.focus()
})
</script>

<template>
 <div class="modal-backdrop" @click.self="close">
  <section ref="dialog" class="backup-modal backup-form" :inert="picker" role="dialog" aria-modal="true" aria-labelledby="backup-upload-title" tabindex="-1" @keydown="keys">
   <h2 id="backup-upload-title">{{loginRequired?'请先登录123云盘':'上传备份到123云盘'}}</h2>
   <template v-if="loginRequired">
    <p>登录后回到备份历史，即可上传此备份。</p>
    <div class="modal-actions">
     <button class="secondary" type="button" @click="close">取消</button>
     <button class="primary" type="button" @click="emit('login')">跳转</button>
    </div>
   </template>
   <template v-else>
   <p>{{row.name}} · {{row.files}} 个文件</p>
   <p v-if="working&&!status?.logged_in" role="status">正在检查登录状态…</p>
   <div v-if="status?.logged_in&&!started" class="cloud-target">
    <span class="cloud-target-path">当前操作目录：{{targetPath}}</span>
    <button type="button" class="secondary" :disabled="working||active" @click="picker=true">选择目录</button>
   </div>
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
    <button v-else class="secondary" type="button" :disabled="working" @click="close">{{started?'关闭':'取消'}}</button>
    <button v-if="!started&&error&&!status?.logged_in" class="primary" type="button" :disabled="working" @click="open">重试</button>
    <button v-else-if="!started&&status?.logged_in" class="primary" type="button" :disabled="working||active" @click="start">开始上传</button>
   </div>
   </template>
  </section>
 </div>
 <CloudFolderPicker v-if="picker" :trail="trail" @close="picker=false" @select="selectFolder" @login-required="picker=false;open()"/>
</template>
