<script setup lang="ts">
import {nextTick,onUnmounted,ref,useId,watch} from 'vue'

withDefaults(defineProps<{label:string;text:string;symbol?:string;circle?:boolean;tone?:string}>(),{symbol:'?',circle:true,tone:'muted'})
const id=useId(),open=ref(false),trigger=ref<HTMLButtonElement|null>(null),panel=ref<HTMLElement|null>(null)
const position=ref({left:'0px',top:'0px'})
let pinned=false,closeTimer:ReturnType<typeof setTimeout>|undefined
function cancelClose(){if(closeTimer)clearTimeout(closeTimer)}
function dismiss(){cancelClose();open.value=false;pinned=false}
async function show(){cancelClose();open.value=true;await nextTick();place()}
function leave(){cancelClose();if(!pinned&&document.activeElement!==trigger.value)closeTimer=setTimeout(dismiss,120)}
function toggle(){if(pinned)dismiss();else{pinned=true;void show()}}
function place(){
 if(!trigger.value||!panel.value)return
 const anchor=trigger.value.getBoundingClientRect(),box=panel.value.getBoundingClientRect()
 const left=Math.max(12,Math.min(anchor.left+anchor.width/2-box.width/2,window.innerWidth-box.width-12))
 const top=anchor.bottom+8+box.height<=window.innerHeight-12?anchor.bottom+8:Math.max(12,anchor.top-box.height-8)
 position.value={left:`${left}px`,top:`${top}px`}
}
function outside(event:PointerEvent){const target=event.target as Node;if(!trigger.value?.contains(target)&&!panel.value?.contains(target))dismiss()}
function key(event:KeyboardEvent){if(event.key==='Escape')dismiss()}
function scroll(event:Event){if(event.target!==panel.value)dismiss()}
function listen(enabled:boolean){
 if(enabled){
  document.addEventListener('pointerdown',outside)
  document.addEventListener('keydown',key)
  window.addEventListener('resize',dismiss)
  window.addEventListener('scroll',scroll,true)
 }else{
  document.removeEventListener('pointerdown',outside)
  document.removeEventListener('keydown',key)
  window.removeEventListener('resize',dismiss)
  window.removeEventListener('scroll',scroll,true)
 }
}
watch(open,visible=>listen(visible))
onUnmounted(()=>{cancelClose();listen(false)})
</script>

<template>
 <span class="help-tip">
  <button ref="trigger" type="button" class="help-trigger" :class="[tone,{'help-question':circle}]" :aria-label="label" :aria-describedby="open?id:undefined" :aria-expanded="open" @mouseenter="show" @mouseleave="leave" @focus="show" @blur="dismiss" @click="toggle">{{symbol}}</button>
  <Teleport to="body"><span v-if="open" :id="id" ref="panel" role="tooltip" class="help-tooltip" :style="position" @mouseenter="cancelClose" @mouseleave="leave">{{text}}</span></Teleport>
 </span>
</template>
