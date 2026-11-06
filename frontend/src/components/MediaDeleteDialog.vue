<script setup lang="ts">
import FeedbackNotice from './FeedbackNotice.vue'
import {errorFeedback} from '../feedback'
import {filterScope} from '../filterPresentation'
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {api,type CleanupItem,type CleanupJob} from '../api'
const props=defineProps<{job:CleanupJob;busy:boolean;readOnly?:boolean}>()
const emit=defineEmits<{close:[];confirm:[id:string]}>()
const rows=ref<CleanupItem[]>([]),offset=ref(0),checked=ref(false),error=ref(''),loading=ref(false)
const preparing=computed(()=>!props.readOnly&&props.job.state==='prepared')
const scope=computed(()=>props.job.backup_ids?.length?'按备份中的原实例与内容清理':filterScope(props.job.filter))
const labels:Record<string,string>={ready:'待删除',recycled:'已回收',skipped:'已跳过',failed:'失败',uncertain:'待核对',pending:'未处理',processing:'处理中'}
const bytes=(n:number)=>`${(n/1024/1024).toLocaleString(undefined,{maximumFractionDigits:1})} MiB`
let generation=0,disposed=false
async function load(){const g=++generation;loading.value=true;error.value='';try{const items=await api().GetMediaDeleteItems(props.job.id,offset.value,60);if(!disposed&&g===generation)rows.value=items}catch(e){error.value=String(e)}finally{if(g===generation)loading.value=false}}
watch(offset,()=>void load());onMounted(()=>void load());onUnmounted(()=>{disposed=true})
</script>
<template>
<div class="modal-backdrop" @click.self="emit('close')"><section class="media-delete-modal" role="dialog" aria-modal="true" :aria-label="preparing?'确认删除媒体':'媒体删除结果'">
 <h2>{{preparing?'确认删除媒体':'媒体删除结果'}}</h2>
 <div class="delete-scroll">
 <p><strong>{{job.source_name}}</strong> · {{scope}}</p><p class="path-text">{{job.root}}</p>
 <p v-if="preparing">已固定 {{job.total}} 个文件：可删除 {{job.ready}} 个（逻辑大小 {{bytes(job.bytes)}}），跳过 {{job.skipped}} 个。不会把确认后新增的文件纳入本次删除。</p>
 <p v-else>回收 {{job.recycled}} 个，跳过 {{job.skipped}} 个，失败 {{job.failed}} 个，待核对 {{job.uncertain}} 个，未执行 {{job.ready+(job.pending??0)}} 个。{{job.state==='prepared'?'尚未删除；请从清理入口重新核对后执行。':job.state==='interrupted'?'上次任务中断，未处理项不会自动继续；请重新选择并核对。':''}}</p>
 <p v-if="job.backup_ids?.length">依据 {{job.backup_ids.length}} 份备份的实际清单，不按当前筛选扩大范围；当时未备份成功、后来新增或改变的文件不清理。本地包可以已删除，其他备份是否可用由你自行确认。</p>
 <p v-if="preparing" class="delete-impact">删除会移除 QQ 本地媒体，聊天中的对应图片、视频等可能无法打开。仅移至 Windows 回收站，失败不改为永久删除；不清空回收站，也不承诺立即释放空间。不会删除备份包或其他副本。</p>
 <FeedbackNotice v-if="error" :feedback="errorFeedback(error)"/>
 <p v-if="loading" role="status">正在读取清单…</p>
 <div class="cleanup-items"><table><thead><tr><th>文件相对路径</th><th>状态</th><th>说明</th></tr></thead><tbody><tr v-for="row in rows" :key="row.item.instance_id"><td>{{row.item.relative_path}}</td><td>{{labels[row.status]||row.status}}</td><td>{{row.detail||'执行前再次核对身份和内容'}}</td></tr></tbody></table></div>
 <div class="cleanup-pagination"><button class="secondary" :disabled="loading||offset===0" @click="offset=Math.max(0,offset-60)">上一页</button><span>{{Math.floor(offset/60)+1}} / {{Math.max(1,Math.ceil(job.total/60))}}</span><button class="secondary" :disabled="loading||offset+60>=job.total" @click="offset+=60">下一页</button></div>
 <label v-if="preparing" class="check-line"><input type="checkbox" v-model="checked"/>{{job.backup_ids?.length?'我已退出 QQ，自行确认保留了可用备份，并确认清理以上原文件':'我已退出 QQ，并确认以上源媒体不再需要'}}</label>
 </div>
 <div class="modal-actions"><button class="secondary" @click="emit('close')">{{preparing?'取消':'关闭'}}</button><button v-if="preparing" class="danger-button" :disabled="busy||!checked||!job.ready||loading||!!error" @click="emit('confirm',job.id)">删除并移至回收站（{{job.ready}}）</button></div>
</section></div>
</template>
