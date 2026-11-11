<script setup lang="ts">
import {computed,onMounted,ref} from 'vue'
import {api,type CloudFolderUpload,type CloudFolderUploadIssue} from '../api'
import FeedbackNotice from './FeedbackNotice.vue'
import RecordPager from './RecordPager.vue'
import {cloudErrorFeedback} from '../cloudFeedback'

const props=defineProps<{task:CloudFolderUpload}>()
const emit=defineEmits<{close:[]}>()
const dialog=ref<HTMLDialogElement|null>(null)
const expanded=ref(false),loading=ref(false),page=ref(1),error=ref('')
const issues=ref<CloudFolderUploadIssue[]>([])
const remaining=computed(()=>props.task.total-props.task.uploaded-props.task.skipped-props.task.failed)
const pages=computed(()=>Math.ceil(props.task.issues/50))

onMounted(()=>dialog.value?.showModal())

async function loadIssues(next:number){
 if(loading.value)return
 loading.value=true
 error.value=''
 try {
  issues.value=await api().GetCloudFolderUploadIssues(props.task.id,(next-1)*50)
  page.value=next
 } catch(e) {
  error.value=String(e)
 } finally {
  loading.value=false
 }
}
function toggleIssues(){
 expanded.value=!expanded.value
 if(expanded.value&&!issues.value.length)void loadIssues(1)
}
function closeResult(){
 dialog.value?.close()
 emit('close')
}
</script>

<template>
 <dialog ref="dialog" class="backup-modal cloud-result-dialog" aria-labelledby="cloud-result-title" @cancel.prevent="closeResult">
  <h2 id="cloud-result-title">文件夹上传结果</h2>
  <p>{{task.name}}</p>
  <p v-if="task.detail">{{task.detail}}</p>
  <p v-if="task.phase==='paused'&&task.current&&task.current!=='.'">{{task.current}}</p>
  <dl>
   <dt>已上传</dt><dd>{{task.uploaded}} 个文件</dd>
   <template v-if="task.skipped"><dt>同名跳过</dt><dd>{{task.skipped}} 个文件</dd></template>
   <template v-if="task.failed"><dt>失败</dt><dd>{{task.failed}} 个文件</dd></template>
   <template v-if="remaining"><dt>未完成</dt><dd>{{remaining}} 个文件</dd></template>
  </dl>
  <button v-if="task.issues" type="button" class="text-button" :aria-expanded="expanded" aria-controls="cloud-upload-issues" @click="toggleIssues">
   {{expanded?'收起明细':`查看跳过与失败明细（${task.issues} 项）`}}
  </button>
  <div v-if="expanded" id="cloud-upload-issues" :aria-busy="loading">
   <FeedbackNotice v-if="error" :feedback="cloudErrorFeedback(error)"/>
   <ul v-if="issues.length" class="cloud-result-issues">
    <li v-for="issue in issues" :key="issue.path">{{issue.path==='.'?task.name:issue.path}} · {{issue.detail}}</li>
   </ul>
   <RecordPager v-if="pages>1&&!loading" :page="page" :more="page<pages" :total="pages" @change="loadIssues"/>
  </div>
  <div class="modal-actions"><button type="button" class="primary" autofocus @click="closeResult">关闭</button></div>
 </dialog>
</template>
