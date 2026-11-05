<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref} from 'vue'
import {api,type CloudFolder} from '../api'
import FeedbackNotice from './FeedbackNotice.vue'
import {cloudErrorFeedback as errorFeedback} from '../cloudFeedback'
const props=defineProps<{trail:CloudFolder[]}>()
const emit=defineEmits<{close:[];select:[trail:CloudFolder[]];loginRequired:[]}>()
const trail=ref(props.trail.map(f=>({...f}))),rows=ref<CloudFolder[]>([]),loading=ref(false),error=ref(''),next=ref(''),more=ref(false)
const dialog=ref<HTMLElement|null>(null),current=computed(()=>trail.value[trail.value.length-1])
let generation=0,disposed=false,previous:HTMLElement|null=null
async function load(append=false){
 const g=++generation;loading.value=true;error.value='';if(!append){rows.value=[];next.value='';more.value=false}
 try{const p=await api().ListCloudFolders(current.value.id,append?next.value:'0');if(disposed||g!==generation)return;rows.value=append?[...rows.value,...p.folders]:p.folders;next.value=p.next;more.value=p.more}
 catch(e){if(!disposed&&g===generation){error.value=String(e);try{const state=await api().GetCloudStatus();if(!disposed&&g===generation&&!state.logged_in)emit('loginRequired')}catch{}}}finally{if(g===generation)loading.value=false}
}
function enter(folder:CloudFolder){trail.value.push(folder);void load()}
function level(i:number){trail.value=trail.value.slice(0,i+1);void load()}
function chooseCurrent(){emit('select',trail.value.map(f=>({...f})))}
function keys(e:KeyboardEvent){if(e.key==='Escape'){e.preventDefault();emit('close')}if(e.key!=='Tab')return;const list=Array.from(dialog.value?.querySelectorAll<HTMLElement>('button:not([disabled]),input:not([disabled]),[tabindex="0"]')||[]);if(!list.length)return;const first=list[0],last=list[list.length-1];if(e.shiftKey&&(document.activeElement===first||document.activeElement===dialog.value)){e.preventDefault();last.focus()}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus()}}
onMounted(async()=>{previous=document.activeElement as HTMLElement|null;await nextTick();dialog.value?.focus();void load()})
onUnmounted(()=>{disposed=true;generation++;previous?.focus()})
</script>
<template>
<div class="modal-backdrop" @click.self="emit('close')">
 <section ref="dialog" class="cloud-folder-dialog" role="dialog" aria-modal="true" aria-labelledby="cloud-folder-title" tabindex="-1" @keydown="keys">
  <h2 id="cloud-folder-title">选择云端目录</h2>
  <nav class="cloud-breadcrumb" aria-label="云端目录"><template v-for="(folder,i) in trail" :key="folder.id"><span v-if="i" aria-hidden="true">/</span><button v-if="i<trail.length-1" type="button" class="text-button" :disabled="loading" @click="level(i)">{{folder.name}}</button><span v-else aria-current="page">{{folder.name}}</span></template></nav>
  <FeedbackNotice v-if="error" :feedback="errorFeedback(error)"/>
  <p v-if="loading&&!rows.length" role="status">正在读取目录…</p>
  <div class="cloud-folder-list"><button v-for="folder in rows" :key="folder.id" type="button" class="cloud-folder-row" :disabled="loading" @click="enter(folder)"><span>{{folder.name}}</span><span aria-hidden="true">›</span></button></div>
  <p v-if="!loading&&!error&&!rows.length&&!more" class="cloud-muted">没有子文件夹</p>
  <div class="cloud-folder-page"><button v-if="more" type="button" class="secondary" :disabled="loading" @click="load(true)">{{loading?'正在读取…':'加载更多'}}</button><button v-if="error" type="button" class="text-button" :disabled="loading" @click="load()">重新读取</button></div>
  <div class="modal-actions"><button type="button" class="secondary" @click="emit('close')">取消</button><button type="button" class="primary" :disabled="loading||!!error" @click="chooseCurrent">选择当前目录</button></div>
 </section>
</div>
</template>
