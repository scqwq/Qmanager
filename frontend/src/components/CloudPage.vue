<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref} from 'vue'
import {api,type CloudFolder,type CloudLocalFile,type CloudQR,type CloudStatus} from '../api'
import FeedbackNotice from './FeedbackNotice.vue'
import {cloudErrorFeedback as errorFeedback} from '../cloudFeedback'
import CloudFolderPicker from './CloudFolderPicker.vue'
const emit=defineEmits<{back:[]}>()
const status=ref<CloudStatus>({logged_in:false,account:'',needs_check:false,remembered:false,note:'',upload:{id:'',phase:'',name:'',total:0,done:0,detail:'',remote_id:''}})
const loginModes = [{id:'qr',name:'扫码'},{id:'password',name:'账号密码'}] as const
const mode=ref<(typeof loginModes)[number]['id']>('qr'),working=ref(false),error=ref(''),message=ref(''),qr=ref<CloudQR|null>(null),qrState=ref(''),account=ref(''),password=ref('')
const trail=ref<CloudFolder[]>([{id:'0',name:'我的文件'}]),picker=ref(false),folderForm=ref(false),folderName=ref(''),localFile=ref<CloudLocalFile|null>(null),nameInput=ref<HTMLInputElement|null>(null)
const uploading=computed(()=>['checking','initializing','uploading','completing','verifying'].includes(status.value.upload.phase))
const target=computed(()=>trail.value[trail.value.length-1]),targetPath=computed(()=>trail.value.map(f=>f.name).join(' / '))
const phaseNames:Record<string,string>={checking:'检查文件',initializing:'准备上传',uploading:'上传中',completing:'完成登记',verifying:'核对结果',complete:'上传完成',failed:'上传未完成',cancelled:'已取消'}
const qrLabels:Record<string,string>={waiting:'等待扫码',scanned:'已扫码，请在手机确认',expired:'二维码已过期',rejected:'已取消扫码'}
const qrLabel=computed(()=>qrLabels[qrState.value]||'')
const progress=computed(()=>status.value.upload.total?Math.min(100,Math.round(status.value.upload.done/status.value.upload.total*100)):0)
const bytes=(n:number)=>n<1024?`${n} B`:`${(n/1024/1024).toLocaleString(undefined,{maximumFractionDigits:2})} MiB`
let disposed=false,qrTimer:ReturnType<typeof setTimeout>|undefined,statusTimer:ReturnType<typeof setTimeout>|undefined,messageTimer:ReturnType<typeof setTimeout>|undefined,modeGeneration=0
function notify(text:string){message.value=text;if(messageTimer)clearTimeout(messageTimer);messageTimer=setTimeout(()=>message.value='',8000)}
function stopQR(){if(qrTimer)clearTimeout(qrTimer);qrTimer=undefined}
function qrImageError(){stopQR();qr.value=null;qrState.value='';error.value='二维码图像未能加载，请刷新二维码'}
async function syncStatus(){const value=await api().GetCloudStatus();if(disposed)return;if(status.value.logged_in&&!value.logged_in){trail.value=[{id:'0',name:'我的文件'}];localFile.value=null;folderForm.value=false;picker.value=false}status.value=value}
async function run(action:()=>Promise<void>){if(working.value)return;working.value=true;error.value='';message.value='';try{await action()}catch(e){if(!disposed){error.value=String(e);await syncStatus().catch(()=>{})}}finally{if(!disposed)working.value=false}}
async function fetchQR(){stopQR();qr.value=null;qrState.value='';const g=modeGeneration;const value=await api().GetCloudQRCode();if(disposed||g!==modeGeneration||mode.value!=='qr')return;qr.value=value;qrState.value='waiting';scheduleQR(value.id,g)}
function scheduleQR(id:string,g:number){qrTimer=setTimeout(async()=>{if(disposed||g!==modeGeneration||mode.value!=='qr')return;try{const result=await api().PollCloudQRCode(id);if(disposed||g!==modeGeneration)return;qrState.value=result.state;status.value=result.status;if(result.state==='logged_in'){qr.value=null;return}if(['waiting','scanned'].includes(result.state))scheduleQR(id,g)}catch(e){if(!disposed&&g===modeGeneration){error.value=String(e);qrState.value='';}}},1500)}
async function changeMode(value:typeof mode.value){if(working.value)return;stopQR();modeGeneration++;mode.value=value;qr.value=null;qrState.value='';password.value='';error.value='';message.value='';await api().CancelCloudQRCode();if(value==='qr')await run(fetchQR)}
async function refreshLogin(){await run(async()=>{const value=await api().RefreshCloudLogin();if(disposed)return;status.value=value;if(uploading.value)scheduleStatus();if(!value.logged_in&&!value.needs_check&&mode.value==='qr')await fetchQR()})}
async function loginPassword(){await run(async()=>{try{await api().LoginCloudPassword(account.value.trim(),password.value);await syncStatus()}finally{password.value=''}})}
async function logout(){await run(async()=>{await api().LogoutCloud();trail.value=[{id:'0',name:'我的文件'}];localFile.value=null;await syncStatus();mode.value='qr';modeGeneration++;await fetchQR()})}
function chooseTarget(value:CloudFolder[]){trail.value=value;picker.value=false;error.value='';message.value=''}
async function folderLoginRequired(){picker.value=false;await syncStatus();error.value=status.value.note||'云盘登录已失效，请重新登录'}
async function newFolder(){error.value='';message.value='';folderName.value='';folderForm.value=true;await nextTick();nameInput.value?.focus()}
async function createFolder(){await run(async()=>{const f=await api().CreateCloudFolder(target.value.id,folderName.value.trim());if(disposed)return;folderForm.value=false;notify(`已新建文件夹“${f.name}”`)})}
async function chooseFile(){await run(async()=>{const f=await api().ChooseCloudFile();if(!disposed&&f.ticket)localFile.value=f})}
async function startUpload(){if(!localFile.value)return;await run(async()=>{await api().StartCloudUpload(target.value.id,localFile.value!.ticket);localFile.value=null;await syncStatus();scheduleStatus()})}
function scheduleStatus(){if(statusTimer)clearTimeout(statusTimer);statusTimer=setTimeout(async()=>{if(disposed)return;try{await syncStatus();if(uploading.value)scheduleStatus()}catch(e){error.value=String(e)}},800)}
function folderKeys(e:KeyboardEvent){if(e.key==='Escape'&&!working.value){e.preventDefault();folderForm.value=false}if(e.key==='Tab'){const root=(e.currentTarget as HTMLElement),els=Array.from(root.querySelectorAll<HTMLElement>('input:not([disabled]),button:not([disabled])'));const first=els[0],last=els[els.length-1];if(e.shiftKey&&document.activeElement===first){e.preventDefault();last?.focus()}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first?.focus()}}}
onMounted(()=>{void refreshLogin()})
onUnmounted(()=>{disposed=true;modeGeneration++;stopQR();if(statusTimer)clearTimeout(statusTimer);if(messageTimer)clearTimeout(messageTimer);password.value='';void api().CancelCloudQRCode().catch(()=>{})})
</script>
<template>
<section class="cloud-page" aria-labelledby="cloud-title">
 <div :inert="picker||folderForm">
 <button type="button" class="text-button logs-back" @click="emit('back')"><span aria-hidden="true">‹</span> 返回设置</button>
 <div class="cloud-heading"><h2 id="cloud-title">云盘登录</h2><button v-if="status.logged_in" type="button" class="text-button" :disabled="working||uploading" @click="logout">退出登录</button></div>
 <FeedbackNotice v-if="error&&!folderForm" :feedback="errorFeedback(error)"/>
 <p v-if="status.note" class="notice" role="status">{{status.note}}</p>
 <p v-if="message" class="notice success" role="status">{{message}}</p>
 <template v-if="!status.logged_in">
  <div class="cloud-login-tabs" role="group" aria-label="登录方式"><button v-for="item in loginModes" :key="item.id" type="button" :class="['secondary',{selected:mode===item.id}]" :aria-pressed="mode===item.id" :disabled="working" @click="changeMode(item.id)">{{item.name}}</button></div>
  <div v-if="mode==='qr'" class="cloud-qr">
   <img v-if="qr" :src="qr.image" width="232" height="232" alt="123云盘登录二维码" @error="qrImageError"/>
   <div v-else class="cloud-qr-empty">{{working?'正在获取二维码…':'登录二维码'}}</div>
   <p v-if="qr">使用123云盘App或微信扫码</p><p v-if="qrLabel" role="status">{{qrLabel}}</p>
   <button type="button" class="secondary" :disabled="working" @click="run(fetchQR)">{{qr?'刷新二维码':'获取二维码'}}</button>
  </div>
  <form v-else class="cloud-login-form" @submit.prevent="loginPassword">
   <label for="cloud-account">手机号或邮箱</label><input id="cloud-account" v-model="account" autocomplete="username" :disabled="working"/>
   <label for="cloud-password">密码</label><input id="cloud-password" v-model="password" type="password" autocomplete="off" :disabled="working"/>
   <div><button type="submit" class="primary" :disabled="working||!account.trim()||!password">{{working?'正在登录…':'登录'}}</button></div>
  </form>
  <button v-if="status.needs_check" type="button" class="text-button" :disabled="working" @click="refreshLogin">重新验证已保存的登录</button>
 </template>
 <template v-else>
  <p class="cloud-account">已登录 · {{status.account}}</p>
  <div class="cloud-target"><span class="cloud-target-path">当前操作目录：{{targetPath}}</span><button type="button" class="secondary" :disabled="working||uploading" @click="picker=true">选择目录</button></div>
  <div class="cloud-actions"><button type="button" class="secondary" :disabled="working||uploading" @click="newFolder">新建文件夹</button><button type="button" class="primary" :disabled="working||uploading" @click="chooseFile">上传文件</button></div>
  <div v-if="localFile" class="cloud-file-choice"><div><strong>{{localFile.name}}</strong><span>{{bytes(localFile.bytes)}}</span></div><button type="button" class="primary" :disabled="working||uploading" @click="startUpload">开始上传</button><button type="button" class="text-button" :disabled="working||uploading" @click="localFile=null">取消选择</button></div>
 </template>
 <section v-if="status.upload.id" class="cloud-upload" aria-label="上传任务">
  <div class="cloud-heading"><h3>{{phaseNames[status.upload.phase]||status.upload.phase}}</h3><button v-if="uploading" type="button" class="text-button" @click="api().CancelCloudUpload()">取消上传</button></div>
  <p class="cloud-upload-name">{{status.upload.name}}</p>
  <template v-if="uploading"><progress :value="progress" max="100" :aria-label="phaseNames[status.upload.phase]"/><p class="cloud-muted" role="status">{{bytes(status.upload.done)}} / {{bytes(status.upload.total)}}</p></template>
  <FeedbackNotice v-if="status.upload.phase==='failed'" :feedback="errorFeedback(status.upload.detail)"/>
  <p v-else-if="status.upload.detail" :class="status.upload.phase==='complete'?'notice success':'notice'" role="status">{{status.upload.detail}}</p>
 </section>
 </div>
 <CloudFolderPicker v-if="picker" :trail="trail" @close="picker=false" @select="chooseTarget" @login-required="folderLoginRequired"/>
 <div v-if="folderForm" class="modal-backdrop" @click.self="!working&&(folderForm=false)"><form class="cloud-folder-dialog cloud-name-dialog" role="dialog" aria-modal="true" aria-labelledby="cloud-name-title" @submit.prevent="createFolder" @keydown="folderKeys"><h2 id="cloud-name-title">新建文件夹</h2><label for="cloud-folder-name">名称</label><input id="cloud-folder-name" ref="nameInput" v-model="folderName" maxlength="254" :disabled="working"/><FeedbackNotice v-if="error" :feedback="errorFeedback(error)"/><div class="modal-actions"><button type="button" class="secondary" :disabled="working" @click="folderForm=false">取消</button><button type="submit" class="primary" :disabled="working||!folderName.trim()">{{working?'正在创建…':'创建'}}</button></div></form></div>
</section>
</template>
