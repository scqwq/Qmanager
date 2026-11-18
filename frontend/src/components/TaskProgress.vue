<script setup lang="ts">
import {computed} from 'vue'
const props=defineProps<{label:string;done?:number;total?:number}>()
const value=computed(()=>props.total&&props.done!==undefined?Math.min(props.total,Math.max(0,props.done)):undefined)
</script>

<template>
 <progress class="task-progress" :value="value" :max="total||1" :aria-label="label"/>
</template>
