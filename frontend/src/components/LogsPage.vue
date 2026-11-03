<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {api,type Diagnostics} from '../api'
import FeedbackNotice from './FeedbackNotice.vue'
import {errorFeedback} from '../feedback'

defineEmits<{back:[]}>()
const data=ref<Diagnostics|null>(null),loading=ref(false),error=ref(''),filter=ref('all')
const entries=computed(()=>data.value?.entries.filter(e=>filter.value==='all'||e.level!=='info')??[])
let disposed=false
const time=(value:string)=>new Date(value).toLocaleString()
async function refresh(){
 loading.value=true;error.value=''
 try{const next=await api().GetDiagnostics();if(!disposed)data.value=next}
 catch(e){if(!disposed)error.value=String(e)}
 finally{if(!disposed)loading.value=false}
}
onMounted(()=>void refresh())
onUnmounted(()=>{disposed=true})
</script>

<template>
<section class="logs-page" aria-labelledby="logs-title">
 <button type="button" class="text-button logs-back" @click="$emit('back')"><span aria-hidden="true">‹</span> 返回设置</button>
 <div class="logs-heading"><h2 id="logs-title">运行日志</h2><button type="button" class="secondary" :disabled="loading" @click="refresh">{{loading?'正在读取…':'刷新'}}</button></div>
 <p class="logs-intro">排查回收中断和应用退出的问题。记录只保存在本机。</p>
 <FeedbackNotice v-if="error" :feedback="errorFeedback(error)"/>
 <p v-if="loading&&!data" role="status">正在读取日志…</p>
 <template v-if="data">
  <p v-if="data.write_note" class="notice warning" role="status">{{data.write_note}}</p>
  <section class="logs-section" aria-labelledby="crashes-title">
   <h3 id="crashes-title">崩溃记录</h3>
   <p v-if="data.system_note" class="logs-muted">{{data.system_note}}</p>
   <p v-if="!data.crashes.length&&!data.crash_text.length" class="logs-muted">{{data.system_note?'本地还没有捕获到崩溃详情。':'未找到最近 7 天的 Windows 崩溃记录，本地也没有捕获到崩溃详情。'}}</p>
   <div v-for="(crash,index) in data.crashes" :key="index" class="log-crash">
    <p><time :datetime="crash.at">{{time(crash.at)}}</time> · {{crash.app}}</p>
    <details><summary>查看错误信息</summary><p>异常代码：<code>{{crash.code}}</code><br/>出错模块：{{crash.module}}<br/>位置：<code>{{crash.offset}}</code></p></details>
   </div>
   <details v-for="(crash,index) in data.crash_text" :key="'text-'+index" class="log-crash">
    <summary>{{time(crash.at)}} · 查看程序崩溃详情</summary>
    <p v-if="crash.truncated" class="logs-muted">详情较长，仅显示前 256 KiB；完整记录保留在本机。</p>
    <pre>{{crash.text}}</pre>
   </details>
  </section>
  <section class="logs-section" aria-labelledby="events-title">
   <div class="logs-heading"><h3 id="events-title">最近运行记录</h3><label class="logs-filter">显示<select v-model="filter"><option value="all">全部记录</option><option value="issues">仅异常与提醒</option></select></label></div>
   <p v-if="!entries.length" class="logs-muted">{{filter==='all'?'还没有运行记录。':'最近记录中没有异常或提醒。'}}</p>
   <ol v-else class="log-entries" aria-label="最近运行记录，最新在前">
    <li v-for="(entry,index) in entries" :key="index" :class="{'log-warning':entry.level==='warning','log-error':entry.level==='error'}">
     <time :datetime="entry.at">{{time(entry.at)}}</time>
     <div class="log-entry-copy"><p>{{entry.detail}}</p><details><summary>定位信息</summary><p>阶段：<code>{{entry.event}}</code><template v-if="entry.task"><br/>任务：<code>{{entry.task}}</code></template><template v-if="entry.item"><br/>文件编号：<code>{{entry.item}}</code></template></p></details></div>
    </li>
   </ol>
  </section>
  <details class="settings-help logs-help"><summary>记录内容与保存位置</summary><p>记录应用启停、任务阶段、回收调用和结果保存，最多显示最近 300 条。运行记录轮换保存，最多 3 份、每份 1 MiB；程序崩溃详情在启动时整理，保留最近 2 份旧文件及本次文件。</p><p>Windows 记录从最近 7 天最多 64 条系统错误中查找，最多显示 5 条 Qman 崩溃。常规记录不包含媒体内容、账号和完整媒体路径；崩溃详情可能包含本机路径，不会自动上传。强制结束、断电或部分原生故障可能来不及留下详情，未找到记录不代表没有崩溃。</p><p class="path-text">{{data.directory}}</p></details>
 </template>
</section>
</template>
