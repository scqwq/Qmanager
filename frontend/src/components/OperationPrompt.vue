<script setup lang="ts">
import {onMounted, ref} from 'vue'

defineProps<{title:string;message:string;confirm?:string;danger?:boolean}>()
const emit=defineEmits<{close:[confirmed:boolean]}>()
const closeButton=ref<HTMLButtonElement|null>(null)
const proceed=ref<HTMLButtonElement|null>(null)
const previousFocus=document.activeElement
onMounted(()=>closeButton.value?.focus())

function close(confirmed:boolean){
 emit('close',confirmed)
 if(previousFocus instanceof HTMLElement)previousFocus.focus()
}
</script>

<template>
 <div class="modal-backdrop" @click.self="close(false)">
  <section class="backup-modal operation-prompt" role="dialog" aria-modal="true" aria-labelledby="operation-title" @keydown.esc.stop="close(false)" @keydown.tab.prevent="confirm&&($event.target===closeButton?proceed:closeButton)?.focus()">
   <h2 id="operation-title">{{title}}</h2>
   <p>{{message}}</p>
   <div class="modal-actions">
    <button ref="closeButton" :class="confirm?'secondary':'primary'" @click="close(false)">{{confirm?'取消':'关闭'}}</button>
    <button v-if="confirm" ref="proceed" :class="danger?'danger-button':'primary'" @click="close(true)">{{confirm}}</button>
   </div>
  </section>
 </div>
</template>
