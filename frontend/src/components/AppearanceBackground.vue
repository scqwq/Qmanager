<script setup lang="ts">
import {onMounted} from 'vue'
import {appearance,backgroundError,backgroundRevision,loadAppearance} from '../appearance'
onMounted(()=>{void loadAppearance()})
</script>
<template>
 <div class="appearance-background" aria-hidden="true">
  <img v-if="appearance.image_id" :key="appearance.image_id+backgroundRevision" :src="'/appearance/'+appearance.image_id+'?v='+backgroundRevision" :style="{objectFit:appearance.fit,visibility:backgroundError?'hidden':'visible'}" alt="" @error="backgroundError='背景图片无法显示，已使用所选背景色。请在设置中重新选择或移除图片。'" @load="backgroundError=''"/>
 </div>
</template>
