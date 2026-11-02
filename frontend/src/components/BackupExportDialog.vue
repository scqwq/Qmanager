<script setup lang="ts">
import FeedbackNotice from './FeedbackNotice.vue'
import {errorFeedback} from '../feedback'
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {api,pathSettingsChanged,type Filter,type Estimate} from '../api'
const props=defineProps<{initial:Filter;submitting?:boolean;submitError?:string;revision?:number}>()
const emit=defineEmits<{close:[];confirm:[filter:Filter,format:string,path:string]}>()
const scope=ref('current'),kind=ref(props.initial.kind),format=ref('directory')
const now=new Date(),currentMonth=`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}`
const first=ref(props.initial.month||currentMonth),last=ref(first.value),year=ref(first.value.slice(0,4))
const estimate=ref<Estimate|null>(null),error=ref(''),loading=ref(false)
const destination=ref(''),choosing=ref(false),destinationError=ref('')
let generation=0,disposed=false
const bytes=(v:number)=>`${(v/1024/1024).toLocaleString(undefined,{maximumFractionDigits:1})} MiB`
const reasons:Record<string,string>={pending:'尚未通过稳定性检查',temporary:'临时资源',empty:'零字节',unknown:'未知格式',invalid_header:'文件头损坏或不支持',unreadable:'无法读取',changed:'扫描时发生变化'}
const candidates=computed(()=>(estimate.value?.files??0)+(estimate.value?.pending??0))
const exclusions=computed(()=>Object.entries(estimate.value?.excluded_reasons??{}).filter(([,n])=>n>0))
const filter=computed<Filter>(()=>{
 const f={...props.initial,kind:kind.value,offset:0}
 if(scope.value!=='current'){f.view='source';f.month='';f.from='';f.to='';f.month_from=scope.value==='year'?`${year.value}-01`:first.value;f.month_to=scope.value==='year'?`${year.value}-12`:last.value}
 return f
})
async function update(quiet=false){
 const current=++generation
 if(!quiet)estimate.value=null
 error.value=''
 if(scope.value==='year'&&!/^\d{4}$/.test(year.value)){error.value='请输入四位年份';loading.value=false;return}
 if(scope.value==='range'&&(!first.value||!last.value||first.value>last.value)){error.value='请填写有效的开始与结束月份';loading.value=false;return}
 loading.value=true
 try{const value=await api().GetEstimate(filter.value);if(!disposed&&current===generation)estimate.value=value}
 catch(e){if(!disposed&&current===generation)error.value=String(e)}
 finally{if(!disposed&&current===generation)loading.value=false}
}
async function choose(){choosing.value=true;destinationError.value='';try{const path=await api().ChooseBackupDestination();if(path){destination.value=path;window.dispatchEvent(new Event(pathSettingsChanged))}}catch(e){destinationError.value=String(e)}finally{choosing.value=false}}
watch(filter,()=>void update(),{deep:true})
onMounted(()=>{
 void update()
 void api().GetBackupDirectory().then(path=>{if(!disposed&&!destination.value)destination.value=path}).catch(e=>{if(!disposed)destinationError.value=String(e)})

})
watch(()=>props.revision,()=>{if(!props.submitting)void update(true)})
onUnmounted(()=>{disposed=true;generation++})
</script>
<template>
<div class="modal-backdrop" @click.self="!submitting&&emit('close')"><section class="backup-modal backup-form" role="dialog" aria-modal="true" aria-label="创建媒体备份">
 <h2>创建媒体备份</h2>
 <div class="backup-options"><label>媒体类型<select aria-label="媒体类型" v-model="kind" :disabled="submitting"><option value="">全部类型</option><option value="image">图片</option><option value="video">视频</option><option value="emoji">收到的表情</option><option value="voice">语音原文件</option></select></label>
 <label>备份范围<select aria-label="备份范围" v-model="scope" :disabled="submitting"><option value="current">当前筛选条件</option><option value="range">来源月份范围</option><option value="year">来源整年</option></select></label></div>
 <div v-if="scope==='range'" class="range-fields"><label>开始月份<input type="month" v-model="first" :disabled="submitting"/></label><label>结束月份<input type="month" v-model="last" :disabled="submitting"/></label></div>
 <label v-if="scope==='year'">来源年份<input type="number" min="1900" max="9999" v-model="year" :disabled="submitting"/></label>
 <p v-if="scope!=='current'">按来源文件夹的月份选择；继续沿用当前角色、状态和大小条件，不代表发送或下载时间。</p>
 <label>保存形式<select aria-label="保存形式" v-model="format" :disabled="submitting"><option value="directory">文件夹</option><option value="zip">ZIP 单文件（便于手动上传网盘）</option></select></label>
 <FeedbackNotice v-if="error||submitError" :feedback="errorFeedback(error||submitError)"/>
 <p v-if="loading&&!estimate" role="status">正在计算备份范围…</p>
 <dl v-if="estimate"><dt>可直接备份</dt><dd>{{estimate.files}} 个 · {{bytes(estimate.bytes)}}</dd><dt>待核对后备份</dt><dd>{{estimate.pending??0}} 个 · {{bytes(estimate.pending_bytes??0)}}</dd><dt>不纳入</dt><dd>{{estimate.excluded}} 项<span v-for="[reason,count] in exclusions" :key="reason" class="backup-exclusion">{{reasons[reason]??reason}}：{{count}} 项</span></dd><dt>临时峰值空间预算</dt><dd>{{bytes(estimate.estimated_peak*(format==='zip'?3:1))}}<small>按候选副本与索引估算；实际分配空间可能不同</small></dd></dl>
 <p v-if="estimate?.pending" class="notice">仅核对本次范围，再复制并校验；变化、不可读或仍不稳定的项目会明确报告。</p>
 <p v-else-if="estimate&&!candidates" class="notice">当前没有可备份文件，请查看排除原因或调整范围。</p>
 <button class="text-button" :disabled="loading||submitting" @click="update()">{{loading?'正在盘点…':'刷新盘点'}}</button>
 <p>备份只复制，不删除源文件；备份不加密。</p><details class="settings-help"><summary>备份内容与空间说明</summary><p>按原目录树逐路径复制，同内容文件各存一份，复制后校验。{{format==='zip'?'ZIP 主要用于打包，不承诺压缩收益；创建时同时保留目录载荷、ZIP 和校验暂存，因此峰值预算更高。':'请整体保留备份文件夹。'}}</p></details>
 <div class="backup-destination"><label>保存目录<input aria-label="备份保存目录" :value="destination" readonly placeholder="请选择 QQ 数据目录以外的位置" :title="destination"/></label><button class="secondary" :disabled="choosing||submitting" @click="choose">{{choosing?'正在选择…':'选择保存目录'}}</button></div>
 <FeedbackNotice v-if="destinationError" :feedback="errorFeedback(destinationError)"/>
 <small>优先使用设置中的默认保存目录；未设置时首次选择会自动保存为默认。可在目录窗口中新建文件夹。</small>
 <div class="modal-actions"><button class="secondary" :disabled="submitting" @click="emit('close')">取消</button><button class="primary" :disabled="submitting||choosing||!candidates||!destination||!!error" @click="emit('confirm',filter,format,destination)">{{submitting?'正在准备…':'核对并备份'}}</button></div>
</section></div>
</template>
