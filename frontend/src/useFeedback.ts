import {computed,onUnmounted,reactive,watch,type Ref} from 'vue'
import type {State} from './api'
import {errorFeedback,resultFeedback,type Feedback,type FeedbackPage} from './feedback'

type Entry=Feedback&{id:number;sourceId:string|null;channel:'backend'|'frontend'|'result'}
export function useFeedback(state:Ref<State>,frontendError:Ref<string>,page:Ref<FeedbackPage>){
 const entries=reactive<Record<FeedbackPage,{error:Entry|null;result:Entry|null}>>({media:{error:null,result:null},history:{error:null,result:null},settings:{error:null,result:null}})
 let origin:FeedbackPage|null=null,lastBackendPage:FeedbackPage|null=null,requestPending=false,lastMessage='',lastError='',serial=0
 const timers=new Map<number,ReturnType<typeof setTimeout>>()
 function clear(view:FeedbackPage,kind:'error'|'result'){
  const entry=entries[view][kind];if(entry){clearTimeout(timers.get(entry.id));timers.delete(entry.id)}entries[view][kind]=null
 }
 function put(feedback:Feedback,kind:'error'|'result',view=feedback.page??origin??page.value,channel:Entry['channel']=kind==='result'?'result':'backend'){
  clear(view,kind)
  const entry:Entry={...feedback,id:++serial,at:new Date().toLocaleTimeString(),sourceId:view==='media'?(state.value.source?.id??null):null,channel}
  if(channel==='backend')lastBackendPage=view
  entries[view][kind]=entry
  if(kind==='result'&&(entry.tone==='success'||entry.tone==='info'))timers.set(entry.id,setTimeout(()=>{if(entries[view].result?.id===entry.id)clear(view,'result')},8000))
 }
 watch(state,next=>{
  if(next.message&&next.message!==lastMessage){
   const message=resultFeedback(next.message)
   if(message&&next.backup&&next.message.startsWith('备份已完成'))message.detail+=`\n保存位置：${next.backup.path}\n已保存 ${next.backup.files} 个文件，${next.backup.excluded} 项未纳入；原因见备份清单。`
   if(message)put(message,'result')
  }
  if(next.error&&next.error!==lastError)put(errorFeedback(next.error),'error')
  if(!next.error&&lastError)for(const view of ['media','history','settings'] as const){const entry=entries[view].error;if(entry?.channel==='backend'&&entry.detail===lastError)clear(view,'error')}
  lastMessage=next.message;lastError=next.error
  if(!requestPending&&next.phase==='ready')origin=null
 })
 watch(frontendError,(value,previous)=>{
  if(value)put(errorFeedback(value),'error',origin??page.value,'frontend')
  else for(const view of ['media','history','settings'] as const){const entry=entries[view].error;if(entry?.channel==='frontend'&&entry.detail===previous)clear(view,'error')}
 })
 const visible=computed(()=>{
  const group=entries[page.value],source=state.value.source?.id??null
  return (['error','result'] as const).flatMap(kind=>{const entry=group[kind];return entry&&(page.value!=='media'||entry.sourceId===source)?[{kind,entry}]:[]})
 })
 function begin(){origin=page.value;requestPending=true;if(entries[page.value].error?.channel==='frontend')clear(page.value,'error');clear(page.value,'result');frontendError.value=''}
 function accepted(){
  requestPending=false
  const view=origin??page.value
  if(state.value.error&&view===lastBackendPage&&!entries[view].error)put(errorFeedback(state.value.error),'error',view)
  if(state.value.phase==='ready')origin=null
 }
 onUnmounted(()=>{for(const timer of timers.values())clearTimeout(timer)})
 return {visible,begin,accepted,dismiss:(kind:'error'|'result')=>clear(page.value,kind)}
}
