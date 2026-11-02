<script setup lang="ts">
import {nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import type {Source,State} from '../api'
const props=defineProps<{source:Source;mode:string;busy:boolean;discovery?:State['discovery']}>()
const emit=defineEmits<{rename:[name:string]}>()
const root=ref<HTMLDetailsElement|null>(null),panel=ref<HTMLElement|null>(null),editing=ref(false),name=ref('')
const placement=ref({left:'0px',top:'calc(100% + 10px)',bottom:'auto',maxHeight:'420px'})
async function toggle(){
 if(!root.value?.open){editing.value=false;return}
 await nextTick()
 if(!root.value||!panel.value)return
 const anchor=root.value.getBoundingClientRect(),width=panel.value.getBoundingClientRect().width
 const below=window.innerHeight-anchor.bottom-20,above=anchor.top-20,up=below<220&&above>below
 const left=Math.max(20,Math.min(anchor.right-width,window.innerWidth-width-20))-anchor.left
 placement.value={left:`${left}px`,top:up?'auto':'calc(100% + 10px)',bottom:up?'calc(100% + 10px)':'auto',maxHeight:`${Math.max(96,Math.min(420,up?above:below))}px`}
}
function close(){if(root.value)root.value.open=false;editing.value=false}
function outside(event:PointerEvent){if(!root.value?.contains(event.target as Node))close()}
function key(event:KeyboardEvent){if(event.key==='Escape'&&root.value?.open){close();root.value.querySelector('summary')?.focus()}}
function edit(){name.value=props.source.name;editing.value=true}
watch(()=>props.source.id,close)
watch(()=>props.source.name,()=>{editing.value=false})
watch(()=>props.busy,(busy,previous)=>{if(previous&&!busy&&name.value===props.source.name)editing.value=false})
onMounted(()=>{document.addEventListener('pointerdown',outside);document.addEventListener('keydown',key);window.addEventListener('resize',close)})
onUnmounted(()=>{document.removeEventListener('pointerdown',outside);document.removeEventListener('keydown',key);window.removeEventListener('resize',close)})
</script>

<template>
 <details ref="root" class="header-source" @toggle="toggle">
  <summary><strong :title="source.name">{{source.name}}</strong><span>来源详情</span></summary>
  <div ref="panel" class="source-popover" :style="placement"><strong>{{mode==='backup'?'当前独立备份':'当前媒体来源'}}</strong><p class="path-text">{{source.resolved}}</p><p v-if="source.linked">这是所选链接解析后的真实目录，请核对。</p><p v-if="discovery">已找到 {{discovery.candidates.length}} 个来源，未读取其他账号的媒体。<span class="path-text">查找范围：{{discovery.root}}</span></p>
   <button v-if="mode==='source'&&!editing" type="button" class="text-button" :disabled="busy" @click="edit">重命名来源</button>
   <form v-if="editing" class="rename-row" @submit.prevent="emit('rename',name)"><input v-model="name" maxlength="60" aria-label="来源名称"/><button class="secondary" :disabled="busy">保存名称</button><button type="button" class="text-button" @click="editing=false">取消</button></form>
  </div>
 </details>
</template>
