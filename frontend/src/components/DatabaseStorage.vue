<script setup lang="ts">
import {computed,onMounted,ref} from 'vue'
import {api,type DatabaseUsage} from '../api'
import OperationPrompt from './OperationPrompt.vue'
import FeedbackNotice from './FeedbackNotice.vue'
import {errorFeedback} from '../feedback'
const props=defineProps<{busy:boolean}>()
const usage=ref<DatabaseUsage|null>(null),working=ref(false),confirming=ref(false),error=ref(''),message=ref('')
const disabled=computed(()=>props.busy||working.value)
const bytes=(n:number)=>`${(n/1024/1024).toLocaleString(undefined,{maximumFractionDigits:1})} MiB`
async function load(){try{usage.value=await api().GetDatabaseUsage()}catch(e){error.value=String(e)}}
async function compact(accepted:boolean){
 confirming.value=false
 if(!accepted)return
 working.value=true;error.value='';message.value=''
 try{
  const result=await api().CompactDatabase()
  usage.value=result.after
  const saved=Math.max(0,result.before.bytes-result.after.bytes)
  message.value=saved?`整理完成，减少 ${bytes(saved)}。`:'整理完成。'
 }catch(e){error.value=String(e);await load()}
 finally{working.value=false}
}
onMounted(()=>void load())
</script>

<template>
 <div class="database-storage">
  <div class="records-actions">
   <span v-if="usage">已用 {{bytes(usage.used)}} · 可复用 {{bytes(usage.reusable)}}</span>
   <button type="button" class="secondary" :disabled="disabled" @click="confirming=true">{{working?'正在整理…':'整理空间'}}</button>
  </div>
  <FeedbackNotice v-if="error" :feedback="errorFeedback(error)"/>
  <p v-if="message" class="notice success" role="status">{{message}}</p>
  <OperationPrompt v-if="confirming" title="整理应用记录空间？" message="回收过期预检和已处理的旧通知，并整理数据库。操作前请保留一份应用记录备份。" confirm="整理" @close="compact"/>
 </div>
</template>
