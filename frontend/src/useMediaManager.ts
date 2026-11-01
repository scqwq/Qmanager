import {computed,ref,watch,type Ref} from 'vue'
import {api,type CleanupJob,type Filter,type Item,type State} from './api'

export function useMediaManager(state:Ref<State>,busy:Ref<boolean>,getFilter:()=>Filter,run:(fn:()=>Promise<unknown>)=>Promise<void>){
 const active=ref(false),ids=ref(new Set<string>()),excluded=ref(new Set<string>()),all=ref(false),dialog=ref<CleanupJob|null>(null),dialogReadOnly=ref(false)
 let waiting=false,previousJob='',backupRequest=false,requestedBackups=''
 function clear(){ids.value=new Set();excluded.value=new Set();all.value=false;dialog.value=null;waiting=false;backupRequest=false}
 function toggle(){if(busy.value)return;clear();active.value=!active.value}
 function isSelected(item:Item){return all.value?!excluded.value.has(item.instance_id):ids.value.has(item.instance_id)}
 function select(item:Item){if(busy.value)return;const target=all.value?excluded:ids;const next=new Set(target.value);if(next.has(item.instance_id))next.delete(item.instance_id);else next.add(item.instance_id);target.value=next}
 function pageSelected(items:Item[]){return items.length>0&&items.every(isSelected)}
 function selectPage(items:Item[]){if(busy.value)return;const remove=pageSelected(items);if(all.value){const next=new Set(excluded.value);for(const item of items){if(remove)next.add(item.instance_id);else next.delete(item.instance_id)};excluded.value=next}else{const next=new Set(ids.value);for(const item of items){if(remove)next.delete(item.instance_id);else next.add(item.instance_id)};ids.value=next}}
 function selectAll(){if(busy.value)return;ids.value=new Set();excluded.value=new Set();all.value=true}
 async function prepare(){previousJob=state.value.cleanup?.id??'';waiting=true;backupRequest=false;dialogReadOnly.value=false;dialog.value=null;await run(()=>api().PrepareMediaDelete({ids:all.value?[]:[...ids.value],exclude_ids:all.value?[...excluded.value]:[],all_matching:all.value,filter:{...getFilter(),offset:0}}))}
 async function prepareBackups(backupIDs:string[]){previousJob=state.value.cleanup?.id??'';waiting=true;backupRequest=true;requestedBackups=JSON.stringify([...new Set(backupIDs)].sort());dialogReadOnly.value=false;dialog.value=null;await run(()=>api().PrepareBackupSourceDelete(backupIDs));if(state.value.phase!=='preparing_delete'&&state.value.cleanup?.id===previousJob){waiting=false;backupRequest=false}}
 async function execute(id:string){dialog.value=null;await run(()=>api().ExecuteMediaDelete(id));ids.value=new Set();all.value=false}
 async function results(jobID?:string){const id=typeof jobID==='string'?jobID:state.value.cleanup?.id;if(id)await run(async()=>{dialogReadOnly.value=true;dialog.value=await api().GetMediaDeleteJob(id)})}
 watch(()=>[state.value.phase,state.value.cleanup?.id,state.value.cleanup?.state],()=>{
  const job=state.value.cleanup
  if(!waiting||state.value.phase!=='ready')return
  const expected=!backupRequest||JSON.stringify([...(job?.backup_ids??[])].sort())===requestedBackups
  if(job?.state==='prepared'&&job.id!==previousJob&&expected){waiting=false;dialog.value=job}
  else if(state.value.error||!job||job.state==='interrupted'){waiting=false;backupRequest=false}
 })
 watch(()=>`${state.value.mode}:${state.value.source?.id}`,()=>{if(backupRequest&&(waiting||(dialog.value?.source_id===state.value.source?.id&&state.value.mode==='source'))){ids.value=new Set();excluded.value=new Set();all.value=false}else{clear()};active.value=false})
 watch(()=>JSON.stringify({...getFilter(),offset:0}),()=>{if(!waiting||!backupRequest)clear()})
 return {active,ids,all,dialog,dialogReadOnly,clear,toggle,select,isSelected,pageSelected,selectPage,selectAll,prepare,prepareBackups,execute,results,manualCount:computed(()=>ids.value.size),excludedCount:computed(()=>excluded.value.size)}
}
