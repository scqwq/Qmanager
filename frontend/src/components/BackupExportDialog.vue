<script setup lang="ts">
import {computed,onMounted,ref,watch} from 'vue'
import {api,type Filter,type Estimate} from '../api'
const props=defineProps<{initial:Filter}>()
const emit=defineEmits<{close:[];confirm:[filter:Filter,format:string]}>()
const scope=ref('current'),kind=ref(props.initial.kind),format=ref('directory')
const now=new Date(),currentMonth=`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}`
const first=ref(props.initial.month||currentMonth),last=ref(first.value),year=ref(first.value.slice(0,4))
const estimate=ref<Estimate|null>(null),error=ref(''),loading=ref(false)
let generation=0
const bytes=(v:number)=>`${(v/1024/1024).toLocaleString(undefined,{maximumFractionDigits:1})} MiB`
const filter=computed<Filter>(()=>{
 const f={...props.initial,kind:kind.value,offset:0}
 if(scope.value!=='current'){f.view='source';f.month='';f.from='';f.to='';f.month_from=scope.value==='year'?`${year.value}-01`:first.value;f.month_to=scope.value==='year'?`${year.value}-12`:last.value}
 return f
})
async function update(){const current=++generation;error.value='';estimate.value=null
 if(scope.value==='year'&&!/^\d{4}$/.test(year.value)){error.value='请输入四位年份';return}
 if(scope.value==='range'&&(!first.value||!last.value||first.value>last.value)){error.value='请填写有效的开始与结束月份';return}
 loading.value=true;try{const value=await api().GetEstimate(filter.value);if(current===generation)estimate.value=value}catch(e){if(current===generation)error.value=String(e)}finally{if(current===generation)loading.value=false}
}
watch(filter,()=>void update(),{deep:true});onMounted(()=>void update())
</script>
<template>
<div class="modal-backdrop" @click.self="emit('close')"><section class="backup-modal backup-form" role="dialog" aria-modal="true" aria-label="创建媒体备份">
 <h2>创建媒体备份</h2>
 <label>媒体类型<select aria-label="媒体类型" v-model="kind"><option value="">全部类型</option><option value="image">图片</option><option value="video">视频</option><option value="emoji">收到的表情</option><option value="voice">语音原文件</option></select></label>
 <label>备份范围<select aria-label="备份范围" v-model="scope"><option value="current">当前筛选条件</option><option value="range">来源月份范围</option><option value="year">来源整年</option></select></label>
 <div v-if="scope==='range'" class="range-fields"><label>开始月份<input type="month" v-model="first"/></label><label>结束月份<input type="month" v-model="last"/></label></div>
 <label v-if="scope==='year'">来源年份<input type="number" min="1900" max="9999" v-model="year"/></label>
 <p v-if="scope!=='current'">按来源文件夹的月份选择；继续沿用当前角色、状态和大小条件，不代表发送或下载时间。</p>
 <label>保存形式<select aria-label="保存形式" v-model="format"><option value="directory">文件夹</option><option value="zip">ZIP 单文件（便于手动上传网盘）</option></select></label>
 <p v-if="error" class="notice error" role="alert">{{error}}</p>
 <p v-if="loading" role="status">正在计算备份范围…</p>
 <dl v-if="estimate"><dt>可备份文件</dt><dd>{{estimate.files}} 个 · {{bytes(estimate.bytes)}}</dd><dt>不纳入媒体</dt><dd>{{estimate.excluded}} 项，原因保存在清单中</dd><dt>目标空间预算</dt><dd>{{bytes(estimate.estimated_peak*(format==='zip'?3:1))}}</dd></dl>
 <p>保留原文件，复制后校验。{{format==='zip'?'ZIP 主要用于打包，不承诺压缩收益；创建和加载时需要额外暂存空间。':'请整体保留备份文件夹。'}}备份不加密。上次导出位置会自动记住。</p>
 <div class="modal-actions"><button class="secondary" @click="emit('close')">取消</button><button class="primary" :disabled="loading||!estimate?.files||!!error" @click="emit('confirm',filter,format)">选择位置并备份</button></div>
</section></div>
</template>
