<script setup lang="ts">
defineProps<{page:number;more:boolean;total?:number}>()
const emit=defineEmits<{change:[page:number]}>()
</script>

<template>
 <nav class="record-pager" aria-label="记录分页">
  <button class="secondary" type="button" :disabled="page<=1" @click="emit('change',page-1)">上一页</button>
  <span>{{page}}<template v-if="total"> / {{total}}</template></span>
  <button class="secondary" type="button" :disabled="!more" @click="emit('change',page+1)">下一页</button>
 </nav>
</template>
