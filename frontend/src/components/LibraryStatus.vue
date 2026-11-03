<script setup lang="ts">
import {computed} from 'vue'
import type {State} from '../api'
import {libraryStatus} from '../libraryStatus'
const props=defineProps<{state:State;busy:boolean;stopping:boolean}>()
defineEmits<{update:[mode:string];stop:[];cancel:[]}>()
const status=computed(()=>libraryStatus(props.state))
const scanning=computed(()=>props.state.phase==='scanning')
const totals=computed(()=>Object.entries({image:'图片',video:'视频',emoji:'收到的表情',voice:'语音'}).map(([kind,label])=>{
 const buckets=(props.state.report?.buckets??[]).filter(bucket=>bucket.kind===kind)
 return {kind,label,files:buckets.reduce((n,bucket)=>n+bucket.files,0),bytes:buckets.reduce((n,bucket)=>n+bucket.bytes,0)}
}))
const bytes=(v:number)=>{if(!v)return '0 B';const u=['B','KiB','MiB','GiB','TiB'];const n=Math.min(4,Math.floor(Math.log(v)/Math.log(1024)));return `${(v/1024**n).toFixed(n?1:0)} ${u[n]}`}
const time=(v?:string)=>v&&!v.startsWith('0001-')?new Date(v).toLocaleString():'尚未记录'
</script>

<template>
 <section class="library-status" :class="status.tone" aria-label="更新与监控状态">
  <div class="status-main">
   <div class="status-copy"><strong>{{status.title}}</strong><p v-if="status.description">{{status.description}}</p><p v-if="status.gap" class="coverage-reason">{{status.gap}}</p><p v-if="status.working" role="status">已处理 {{state.scanned.toLocaleString()}} 项<span v-if="['preparing_backup','preparing_delete'].includes(state.phase)"> / {{state.check_total??0}} 项</span><template v-if="!['preparing_backup','preparing_delete'].includes(state.phase)"> · {{bytes(state.bytes)}}</template></p></div>
   <div v-if="state.mode==='source'" class="status-actions">
    <button v-if="scanning||state.watching||state.background_stage" type="button" class="secondary" :disabled="busy||stopping||state.stopping" @click="$emit('stop')">{{stopping||state.stopping?'正在停止…':scanning?'终止更新':'暂停监控'}}</button>
    <button type="button" class="secondary" :disabled="busy||scanning" title="补充新文件并开始监控，不全面检查旧文件" @click="$emit('update','fast')">快速更新</button>
    <button type="button" class="secondary" :disabled="busy||scanning" title="检查当前账号全部受支持类型和月份，不扫描其他账号或整块磁盘" @click="$emit('update','full')">全盘更新</button>
   </div>
  </div>
  <button v-if="status.working&&!scanning" type="button" class="text-button status-cancel" @click="$emit('cancel')">取消当前任务</button>
  <details v-if="state.report" class="library-status-details"><summary>盘点与检查详情<span v-if="state.mode==='source'"> · {{state.watching?'监控中':'监控已暂停'}}</span></summary>
   <p>来源合计 {{state.report.files.toLocaleString()}} 个文件 · 逻辑大小 {{bytes(state.report.bytes)}}</p>
   <div class="type-totals"><span v-for="item in totals" :key="item.kind">{{item.label}} {{item.files.toLocaleString()}} 个 · {{bytes(item.bytes)}}</span></div>
   <template v-if="state.mode==='source'"><p>上次全盘检查：{{time(state.last_full_scan)}} · 应用索引 {{bytes(state.index_bytes)}}</p><p v-if="!state.cached">这次实际检查 {{state.report.inspected??0}} 项 · 沿用未检查旧记录 {{state.report.known_skipped??0}} 项 · 更新 {{state.report.indexed_updates}} 项</p><p v-if="state.coverage">原始监控说明：{{state.coverage}}</p><p>预览、备份和回收前仍会核实实际文件；文件稳定和文件头识别不代表完整下载。</p></template>
   <p>最近记录：{{time(state.report.finished_at)}} · {{state.report.issue_count}} 项检查问题</p>
  </details>
 </section>
</template>
