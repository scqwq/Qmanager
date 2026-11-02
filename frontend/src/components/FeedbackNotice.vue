<script setup lang="ts">
import type {Feedback} from '../feedback'
withDefaults(defineProps<{feedback:Feedback;dismissible?:boolean}>(),{dismissible:false})
defineEmits<{dismiss:[]}>()
</script>

<template>
 <section class="feedback-notice" :class="feedback.tone" :role="feedback.tone==='error'||feedback.tone==='warning'?'alert':'status'">
  <div class="feedback-copy"><strong>{{feedback.title}}</strong><p v-if="feedback.advice">{{feedback.advice}}</p><details v-if="feedback.detail&&feedback.detail.replace(/^Error:\s*/,'')!==feedback.title"><summary>{{feedback.tone==='error'?'查看具体原因':'查看详细说明'}}<span v-if="feedback.at"> · {{feedback.at}}</span></summary><p class="feedback-detail">{{feedback.detail}}</p></details></div>
  <button v-if="dismissible" type="button" class="text-button feedback-close" aria-label="关闭提示" @click="$emit('dismiss')">×</button>
 </section>
</template>
