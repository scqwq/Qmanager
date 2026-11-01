<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {api,type BackupRecord} from '../api'
const props=defineProps<{busy:boolean}>()
const emit=defineEmits<{load:[id:string];relocate:[id:string];view:[id:string];remove:[ids:string[]];clean:[ids:string[]];cleanResults:[jobID:string]}>()
const rows=ref<BackupRecord[]>([]),checked=ref<string[]>([]),error=ref(''),confirming=ref(false)
const selectedRecords=computed(()=>rows.value.filter(r=>checked.value.includes(r.id)))
const selected=computed(()=>selectedRecords.value.filter(r=>r.status!=='deleted'))
const cleanable=computed(()=>selectedRecords.value.filter(r=>r.files>0))
const selectedBytes=computed(()=>selected.value.reduce((sum:number,row:BackupRecord)=>sum+row.bytes,0))
const labels:Record<string,string>={available:'本地位置已登记',deleted:'本地包已删除',deleting:'本地包删除中 / 待继续',delete_failed:'本地包删除未完成',unavailable:'本地位置不可用'}
const bytes=(v:number)=>`${(v/1024/1024).toLocaleString(undefined,{maximumFractionDigits:1})} MiB`
function scope(r:BackupRecord){const f=r.selection;const names:Record<string,string>={image:'图片',video:'视频',emoji:'收到的表情',voice:'语音'};return `${names[f.kind]||'全部类型'} · ${f.month_from||f.month_to?`${f.month_from||'不限'} 至 ${f.month_to||'不限'}`:f.month||'全部月份'}${f.view==='observed'?' · 发现日期视图':''}${f.from||f.to?` · ${f.from||'不限'} 至 ${f.to||'不限'}`:''}`}
let loading=false,disposed=false
async function refresh(){if(loading||disposed)return;loading=true;try{const result=await api().GetBackupHistory();if(!disposed){rows.value=result;error.value=''}}catch(e){if(!disposed)error.value=String(e)}finally{loading=false}}
function toggleAll(event:Event){checked.value=(event.target as HTMLInputElement).checked?rows.value.slice(0,100).map(r=>r.id):[]}
function remove(){const ids=selected.value.map(r=>r.id);confirming.value=false;checked.value=[];emit('remove',ids)}
function clean(){const ids=cleanable.value.map(r=>r.id);checked.value=[];emit('clean',ids)}
watch(()=>props.busy,(current,previous)=>{if(previous&&!current)void refresh()})
onMounted(()=>void refresh());onUnmounted(()=>{disposed=true})
</script>
<template>
<section class="backup-history">
 <div class="history-toolbar"><div><h1>备份历史</h1><p>备份只复制。清理原文件与删除本地备份包是两个独立操作，均需分别确认。</p></div><div class="history-batch-actions"><button class="danger-button" :disabled="busy||!cleanable.length||cleanable.length>100" @click="clean">清理选中备份的原文件（{{cleanable.length}}）</button><button class="danger-button" :disabled="busy||!selected.length||selected.length>100" @click="confirming=true">删除本地备份包（{{selected.length}}）</button></div></div>
 <p v-if="error" class="notice error" role="alert">{{error}}</p><p v-if="!rows.length" class="ready-note">暂无备份记录。创建备份或通过“加载备份”选择已有备份后，会在这里登记。</p>
 <div v-else class="history-table-wrap"><table class="history-table"><thead><tr><th><input type="checkbox" aria-label="选择最多一百份备份记录" :disabled="busy" @change="toggleAll"/></th><th>备份范围 / 位置</th><th>时间与大小</th><th>本地包 / 原文件清理状态</th><th>操作</th></tr></thead><tbody><tr v-for="row in rows" :key="row.id"><td><input type="checkbox" :value="row.id" v-model="checked" :disabled="busy" :aria-label="`选择备份 ${row.name}`"/></td><td><strong>{{row.name}} · {{scope(row)}}</strong><p class="path-text">{{row.path}}</p><small>{{row.format==='zip'?'ZIP 文件':'文件夹'}} · {{row.files}} 个媒体引用</small></td><td>{{new Date(row.created_at).toLocaleString()}}<p>{{bytes(row.bytes)}}</p></td><td>{{labels[row.status]||row.status}}<p>{{row.detail}}</p><p>{{row.source_cleanup_detail||'尚无原文件清理任务'}}</p></td><td><div class="history-row-actions"><button class="secondary" :disabled="busy||!row.files" @click="emit('clean',[row.id])">清理对应原文件</button><button v-if="row.source_cleanup_job" class="text-button" :disabled="busy" @click="emit('cleanResults',row.source_cleanup_job)">查看原文件清理结果</button><button class="secondary" :disabled="busy||row.status==='deleted'||row.status==='deleting'" @click="emit('load',row.id)">加载备份</button><button class="secondary" :disabled="busy" @click="emit('relocate',row.id)">重新定位</button><button class="text-button" :disabled="busy" @click="emit('view',row.id)">查看已加载媒体</button></div></td></tr></tbody></table></div>
 <div v-if="confirming" class="modal-backdrop" @click.self="confirming=false"><section class="backup-modal" role="dialog" aria-modal="true" aria-label="确认批量删除本地备份包"><h2>永久删除 {{selected.length}} 份本地备份包？</h2><p>预计删除 {{bytes(selectedBytes)}}（按记录统计，实际删除前重新校验）。此操作不清理原文件或独立副本，不经过回收站；请确认仍保有需要的备份。历史清单继续保留，本地包已删除的记录不重复处理。</p><ul class="delete-review"><li v-for="row in selected" :key="row.id">{{row.name}}<span class="path-text">{{row.path}}</span></li></ul><div class="modal-actions"><button class="secondary" @click="confirming=false">取消</button><button class="danger-button" :disabled="props.busy" @click="remove">永久删除这些本地备份包</button></div></section></div>
</section>
</template>
