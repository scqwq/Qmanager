<script setup lang="ts">
import {ref} from 'vue'
import {api,type CleanupAttempt} from '../api'
const props=defineProps<{job:string;instance:string}>()
const rows=ref<CleanupAttempt[]>([]),error=ref(''),loaded=ref(false)
const labels:Record<string,string>={processing:'处理中',recycled:'已回收',failed:'失败',uncertain:'结果待核对'}
async function load(event:Event){
 if(!(event.target as HTMLDetailsElement).open||loaded.value)return
 try{rows.value=await api().GetCleanupAttempts(props.job,props.instance);loaded.value=true}
 catch(e){error.value=String(e)}
}
</script>

<template>
 <details @toggle="load"><summary>处理记录</summary>
  <p v-for="row in rows" :key="row.attempt">{{row.at?new Date(row.at).toLocaleString():'时间未知'}} · {{labels[row.status]??row.status}}<template v-if="row.detail"> · {{row.detail}}</template></p>
  <p v-if="error" role="alert">{{error}}</p>
 </details>
</template>
