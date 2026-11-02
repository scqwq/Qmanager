<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {api,type AppearanceSettings} from '../api'
import {applyAppearance,appearanceError,backgroundError,commitAppearance,defaultAppearance,loadAppearance,palettes,refreshBackground,retryAppearance,revertAppearance,savedAppearance} from '../appearance'
import FeedbackNotice from './FeedbackNotice.vue'
import {errorFeedback} from '../feedback'

const draft=ref(defaultAppearance()),loaded=ref(false),pending=ref(false),error=ref(''),message=ref('')
const dirty=computed(()=>JSON.stringify(draft.value)!==JSON.stringify(savedAppearance.value))
let disposed=false
watch(draft,value=>{if(loaded.value){applyAppearance(value);message.value=''}},{deep:true,flush:'sync'})
async function load(){await loadAppearance();if(!disposed){draft.value={...savedAppearance.value};loaded.value=true}}
async function retry(){pending.value=true;try{await retryAppearance();if(!disposed)draft.value={...savedAppearance.value}}finally{pending.value=false}}
function theme(value:'light'|'dark'){
 draft.value={...draft.value,theme:value,color:value==='light'?'mist':'slate'}
}
async function choose(){
 pending.value=true;error.value=''
 try{const image=await api().ChooseAppearanceImage();if(disposed){await api().DiscardAppearancePreview();return}if(image.id){draft.value={...draft.value,image_id:image.id,image_name:image.name,panel_opacity:!draft.value.image_id&&draft.value.panel_opacity===100?90:draft.value.panel_opacity};refreshBackground()}}
 catch(e){if(!disposed)error.value=String(e)}finally{pending.value=false}
}
function remove(){draft.value={...draft.value,image_id:'',image_name:''};error.value=''}
async function reset(){draft.value={...savedAppearance.value};error.value='';message.value='';try{await api().DiscardAppearancePreview()}catch(e){error.value=String(e)}}
function defaults(){draft.value=defaultAppearance();error.value=''}
async function save(){
 pending.value=true;error.value='';message.value=''
 try{const value=await api().SaveAppearance({...draft.value});commitAppearance(value);if(!disposed){draft.value={...value};message.value='外观已保存。'}}
 catch(e){if(!disposed)error.value=String(e)}finally{pending.value=false}
}
onMounted(()=>{void load()})
onUnmounted(()=>{disposed=true;revertAppearance();void api().DiscardAppearancePreview().catch(()=>{})})
</script>
<template>
 <section class="appearance-settings" aria-labelledby="appearance-heading">
  <h2 id="appearance-heading">外观</h2>
  <FeedbackNotice v-if="error||appearanceError" :feedback="errorFeedback(error||appearanceError)"/>
  <button v-if="appearanceError" type="button" class="text-button" :disabled="pending" @click="retry">重新读取外观</button>
  <p v-if="backgroundError" class="notice warning" role="status">{{backgroundError}}</p>
  <p v-if="message" class="notice success" role="status">{{message}}</p>
  <p v-if="!loaded" role="status">正在读取外观…</p>
  <form v-else class="settings-form appearance-form" @submit.prevent="save">
   <fieldset class="appearance-field" :disabled="pending">
    <legend>背景主题</legend>
    <div class="appearance-options"><label v-for="v in (['light','dark'] as const)" :key="v"><input type="radio" name="appearance-theme" :value="v" :checked="draft.theme===v" @change="theme(v)"/>{{v==='light'?'浅色':'深色'}}</label></div>
   </fieldset>
   <fieldset class="appearance-field" :disabled="pending">
    <legend>背景颜色</legend>
    <div class="appearance-options appearance-colors"><label v-for="p in palettes[draft.theme]" :key="p.id" :class="{chosen:draft.color===p.id}"><input v-model="draft.color" type="radio" name="appearance-color" :value="p.id"/><span class="appearance-swatch" :style="{backgroundColor:p.color}" aria-hidden="true"></span>{{p.name}}</label></div>
   </fieldset>
   <fieldset class="appearance-field" :disabled="pending">
    <legend>背景图</legend>
    <div class="appearance-image-actions"><button type="button" class="secondary" @click="choose">{{draft.image_id?'更换图片':'选择图片'}}</button><button v-if="draft.image_id" type="button" class="text-button" @click="remove">移除图片</button><span>{{draft.image_name||'未选择'}}</span></div>
    <p class="appearance-help">静态 PNG、JPEG，最大 12 MiB。保存后保留本地副本。</p>
    <div v-if="draft.image_id" class="appearance-fit"><label for="appearance-fit">显示方式</label><select id="appearance-fit" v-model="draft.fit"><option value="cover">铺满裁切</option><option value="contain">完整显示</option></select></div>
   </fieldset>
   <fieldset class="appearance-field" :disabled="pending">
    <legend>面板不透明度</legend>
    <div class="appearance-opacity"><input id="panel-opacity" v-model.number="draft.panel_opacity" aria-label="面板不透明度" type="range" min="0" max="100" step="1"/><output for="panel-opacity">{{draft.panel_opacity}}%</output></div>
    <p class="appearance-help">0% 透明，100% 实色。文字、控件和弹窗保持清楚。</p>
   </fieldset>
   <div class="settings-actions"><button type="submit" class="primary" :disabled="pending||(!dirty&&!appearanceError)">{{pending?'请稍候…':'保存外观'}}</button><button type="button" class="secondary" :disabled="pending||!dirty" @click="reset">撤销修改</button><button type="button" class="text-button" :disabled="pending" @click="defaults">恢复默认</button><small>修改即时预览，离开此页撤销未保存修改。</small></div>
  </form>
 </section>
</template>
