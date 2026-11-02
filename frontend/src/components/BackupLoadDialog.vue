<script setup lang="ts">
import FeedbackNotice from './FeedbackNotice.vue'
import {errorFeedback} from '../feedback'
import {ref} from 'vue'
import {api,pathSettingsChanged,type BackupPreview} from '../api'
const props=defineProps<{preview:BackupPreview}>()
const emit=defineEmits<{close:[];confirm:[id:string,target:string]}>()
const target=ref(props.preview.target),exited=ref(false),choosing=ref(false),error=ref('')
const targetNote=ref(props.preview.target_note??'')
async function choose(){choosing.value=true;error.value='';try{const path=await api().ChooseRestoreTarget();if(path){target.value=path;targetNote.value='';exited.value=false;window.dispatchEvent(new Event(pathSettingsChanged))}}catch(e){error.value=String(e)}finally{choosing.value=false}}
</script>
<template>
<div class="modal-backdrop" @click.self="emit('close')"><section class="backup-modal backup-form" role="dialog" aria-modal="true" aria-label="加载备份">
 <h2>加载备份</h2><p>{{preview.record.name}} · {{preview.record.files}} 个文件</p><p class="path-text">{{preview.record.path}}</p>
 <template v-if="preview.restore_prefix"><p>补回路径：所选 Tencent Files / <strong>{{preview.restore_prefix}}</strong> / 媒体相对路径</p>
 <p v-if="targetNote">{{targetNote}}</p><label>目标 Tencent Files<input aria-label="目标 Tencent Files" readonly :value="target" placeholder="未选择时，仅保存到 Qman"/></label><div class="inline-actions"><button class="secondary" :disabled="choosing" @click="choose">选择目标文件夹</button><button class="text-button" @click="target='';targetNote='';exited=false">仅保存到 Qman</button></div>
 <label v-if="target" class="check-line"><input type="checkbox" v-model="exited"/>我已手动退出 QQ，并确认上面的账号与目标目录</label></template>
 <p v-else>此备份没有可验证的账号路径映射，将复制到 Qman 独立媒体库，不猜测账号目录。</p>
 <p>首次选择目标且设置中尚无默认 QQ 目录时，会自动同步到设置；已有默认值不被单次选择覆盖。</p><p>相同内容跳过；同路径不同内容保留原文件并另存备份版本。写回失败时尝试保存到 Qman 独立媒体库。不会把清单复制进 QQ 目录，也不判断 QQ 是否识别。</p>
 <FeedbackNotice v-if="error" :feedback="errorFeedback(error)"/>
 <div class="modal-actions"><button class="secondary" @click="emit('close')">取消</button><button class="primary" :disabled="choosing||(!!target&&!!preview.restore_prefix&&!exited)" @click="emit('confirm',preview.record.id,preview.restore_prefix?target:'')">加载备份</button></div>
</section></div>
</template>
