<script setup lang="ts">
import {computed,onMounted,onUnmounted,reactive,ref,watch} from 'vue'
import {api,hasDesktop,type Item,type Page,type State,type Bucket,type BackupPreview,type History,type Filter} from './api'
import BackupHistory from './components/BackupHistory.vue'
import SettingsPage from './components/SettingsPage.vue'
import BackupExportDialog from './components/BackupExportDialog.vue'
import BackupLoadDialog from './components/BackupLoadDialog.vue'
import MediaDeleteDialog from './components/MediaDeleteDialog.vue'
import {useMediaManager} from './useMediaManager'
import {useFeedback} from './useFeedback'
import {libraryStatus} from './libraryStatus'
import type {FeedbackPage} from './feedback'
import FeedbackNotice from './components/FeedbackNotice.vue'
import SourceDetails from './components/SourceDetails.vue'
import LibraryStatus from './components/LibraryStatus.vue'
import AppearanceBackground from './components/AppearanceBackground.vue'
const historyPage=ref(false),exportFilter=ref<Filter|null>(null),loadPreview=ref<BackupPreview|null>(null)
const settingsPage=ref(false)
const state=ref<State>({phase:'idle',mode:'source',source:null,sources:[],scanned:0,bytes:0,report:null,watching:false,coverage:'',revision:0,error:'',message:'',backup:null,excluded:0,cached:false,last_full_scan:'',reconcile_seconds:120,index_bytes:0})
const page=ref<Page>({items:[],total:0,bytes:0,baseline_files:0}),buckets=ref<Bucket[]>([])
const visibleFiles=computed(()=>buckets.value.reduce((count,b)=>count+b.files,0))
const year=ref('')
const sidebarPeriod=ref<'month'|'year'>('month')
const kind=ref(''),month=ref(''),role=ref(''),status=ref(''),size=ref(''),offset=ref(0),view=ref('source'),from=ref(''),to=ref('')
const includeBaseline=ref(false),querying=ref(false)
const error=ref(''),action=ref(false),desktop=ref(hasDesktop()),listing=ref(false),selected=ref<Item|null>(null)
const mediaURL=ref(''),playError=ref(false),history=ref<History[]>([]),failed=ref(new Set<string>())
let timer:ReturnType<typeof setInterval>|undefined,polling=false,generation=0,disposed=false
const busy=computed(()=>action.value||['preparing_backup','backup','verifying','discovering','restoring','deleting','preparing_delete','recycling'].includes(state.value.phase))
const scanning=computed(()=>state.value.phase==='scanning')
const currentPage=computed<FeedbackPage>(()=>settingsPage.value?'settings':historyPage.value?'history':'media')
const feedback=useFeedback(state,error,currentPage)
const progressTitle=computed(()=>libraryStatus(state.value).title)
const restoreIssues=computed(()=>state.value.restore?.placements.filter(p=>p.error)??[])
const kinds=[{id:'image',name:'图片'},{id:'video',name:'视频'},{id:'emoji',name:'收到的表情'},{id:'voice',name:'语音'}]
const roles:Record<string,string>={original:'原件',preview:'缩略图',cover:'视频封面',temporary:'临时资源',unknown:'角色未知'}
const statuses:Record<string,string>={recognized:'已识别格式（文件头）',unknown:'未知格式',empty:'零字节',invalid_header:'无法识别文件头',unreadable:'无法读取',changed:'扫描时发生变化'}
const methods:Record<string,string>={baseline:'扫描前已存在',live_event:'监控中首次发现',rescan_after_gap:'中断或核对扫描后发现',user_reported:'用户标注'}
const issues:Record<string,string>={directory_limit:'文件夹数量达到本次查找上限',directory_unreadable_or_link:'文件夹无法读取，或为默认跳过的链接',link_skipped:'这是链接，未继续读取',stat_failed:'无法读取文件信息',non_regular_skipped:'已跳过特殊文件',file_limit:'文件数量达到本次处理上限',unreadable:'无法读取',changed:'文件在检查时变化，需要再检查',directory_read_failed:'这个文件夹没有完整读完'}
const eventNames:Record<string,string>={seen:'首次记录',stable:'通过稳定性检查',superseded:'被新版本替换',rename:'文件位置变化',missing:'核对时缺失',user_recycled:'用户移至回收站'}
const fmtTime=(v:string|null|undefined)=>v&&!v.startsWith('0001-')?new Date(v).toLocaleString():'未知'
const bytes=(v:number)=>{if(!v)return '0 B';const u=['B','KiB','MiB','GiB','TiB'];const i=Math.min(4,Math.floor(Math.log(v)/Math.log(1024)));return `${(v/1024**i).toFixed(i?1:0)} ${u[i]}`}
const name=(i:Item)=>i.relative_path.split('/').at(-1)??i.relative_path
const kindName=(id:string)=>kinds.find(k=>k.id===id)?.name??id
function readiness(item:Item){
 if(item.status!=='recognized')return statuses[item.status]??'状态未知'
 if(item.role==='temporary')return '临时资源'
 return item.stability==='stable_heuristic'?'':item.stability==='unstable'?'文件尚不稳定':'待稳定性核对'
}
const sourceLabel=(s:{name:string;root:string})=>{const parts=s.root.split(String.fromCharCode(92)).join('/').split('/');const at=parts.map(p=>p.toLowerCase()).lastIndexOf('nt_data');const account=at>=2&&parts[at-1].toLowerCase()==='nt_qq'?parts[at-2]:'';return account&&s.name!==account?`${s.name} · ${account}`:s.name}
const thumb=(i:Item)=>`/media/thumbnail/${encodeURIComponent(i.id)}${i.cover_id?`?cover=${encodeURIComponent(i.cover_id)}&revision=${state.value.revision}`:''}`
const failPreview=(id:string)=>{failed.value=new Set([...failed.value,id])}
const title=computed(()=>month.value?`${month.value==='__unknown__'?'月份未知':month.value} · ${kind.value?kindName(kind.value):'全部类型'}`:kind.value?kindName(kind.value):'全部类型')
const availableMonths=computed(()=>{const m=new Map<string,number>();for(const b of buckets.value){if(kind.value&&b.kind!==kind.value)continue;m.set(b.month,(m.get(b.month)??0)+b.files)}return [...m].sort(([a],[b])=>b.localeCompare(a))})
const availableYears=computed(()=>{const y=new Map<string,number>();for(const [m,count] of availableMonths.value){const value=m.slice(0,4);if(/^\d{4}$/.test(value))y.set(value,(y.get(value)??0)+count)}return [...y].sort(([a],[b])=>b.localeCompare(a))})
const years=computed(()=>availableYears.value.map(([y])=>y))
const months=computed(()=>availableMonths.value.filter(([m])=>!year.value||m.startsWith(year.value+'-')))
function filter():Filter{return {include_baseline:includeBaseline.value,kind:kind.value,month:month.value,month_from:year.value?year.value+'-01':'',month_to:year.value?year.value+'-12':'',role:role.value,status:status.value,min_bytes:Number(size.value)||0,max_bytes:0,offset:offset.value,limit:60,view:view.value,from:from.value,to:to.value}}
async function load(){const id=++generation;const source=state.value.source?.id;querying.value=true;if(from.value&&to.value&&from.value>to.value){error.value='开始日期不能晚于结束日期';querying.value=false;return}error.value='';try{const [p,bs]=await Promise.all([api().GetPage(filter()),api().GetFilterBuckets(filter())]);if(id===generation&&!disposed&&source===state.value.source?.id){page.value=p;buckets.value=bs}}catch(e){error.value=String(e)}finally{if(id===generation)querying.value=false}}
function selectYear(){month.value='';from.value='';to.value=''};
function selectMonth(value:string){month.value=value;if(view.value==='observed'){from.value='';to.value=''}}
function selectSidebarYear(value:string){year.value=value;selectYear()}
function queryDate(){year.value='';month.value='';offset.value=0;void load()}
function resetFilters(){year.value='';kind.value='';month.value='';from.value='';to.value='';role.value='';status.value='';size.value='';offset.value=0;void load()}
function showInitialImport(){includeBaseline.value=true;offset.value=0}
async function poll(){if(polling||!hasDesktop()||disposed)return;polling=true;try{const revision=state.value.revision;state.value=await api().GetState();if(revision!==state.value.revision)await load();void api().UIReady()}catch(e){error.value=String(e)}finally{polling=false}}
async function run(fn:()=>Promise<unknown>){feedback.begin();action.value=true;generation++;error.value='';try{await fn();state.value=await api().GetState();await load()}catch(e){error.value=String(e)}finally{action.value=false;feedback.accepted()}}
const manager=reactive(useMediaManager(state,busy,filter,run))
const selectedCount=computed(()=>manager.all?Math.max(0,page.value.total-manager.excludedCount):manager.manualCount)
const choose=()=>run(async()=>{const path=await api().ChooseSource();if(path){settingsPage.value=false;historyPage.value=false;year.value='';failed.value=new Set();selected.value=null;month.value='';offset.value=0}})
const start=(mode:string)=>run(async()=>{failed.value=new Set();await api().StartUpdate(mode)})
const stopping=ref(false)
async function terminateUpdate(){stopping.value=true;try{await run(()=>api().StopUpdate())}finally{stopping.value=false}}
const cancel=()=>run(()=>api().CancelScan())
const openBackup=(expectedID='')=>run(async()=>{const preview=await api().ChooseBackup(expectedID);if(preview)loadPreview.value=preview})
const loadHistory=(id:string)=>run(async()=>{loadPreview.value=await api().PreviewHistoryBackup(id)})
const viewLoaded=(id:string)=>run(async()=>{await api().ViewLoadedBackup(id);settingsPage.value=false;historyPage.value=false;year.value='';failed.value=new Set();selected.value=null;view.value='source';month.value='';from.value='';to.value='';offset.value=0})
const removeBackups=(ids:string[])=>run(()=>api().DeleteBackups(ids))
const forgetBackup=(id:string)=>run(()=>api().DeleteBackupHistory(id))
const checkBackupOriginals=(id:string)=>run(()=>api().CheckBackupOriginals(id))
const importedDefault=()=>run(async()=>{settingsPage.value=false;historyPage.value=false;year.value='';month.value='';offset.value=0;failed.value=new Set();selected.value=null})
async function confirmLoad(id:string,target:string){loadPreview.value=null;await run(async()=>{await api().LoadBackup(id,target);settingsPage.value=false;historyPage.value=false;year.value='';failed.value=new Set();selected.value=null;view.value='source';month.value='';from.value='';to.value='';offset.value=0})}
const switchSource=(event:Event)=>{const id=(event.target as HTMLSelectElement).value;if(id)void run(async()=>{await api().SwitchSource(id);settingsPage.value=false;historyPage.value=false;year.value='';failed.value=new Set();selected.value=null;month.value='';offset.value=0})}
const backupError=ref(''),backupSubmitting=ref(false)
function prepareBackup(){backupError.value='';exportFilter.value={...filter(),offset:0}}
async function backup(f:Filter,format:string,path:string){feedback.begin();backupSubmitting.value=true;action.value=true;backupError.value='';try{await api().CreateBackupAt(f,format,path);exportFilter.value=null;state.value=await api().GetState();await load()}catch(e){backupError.value=String(e)}finally{backupSubmitting.value=false;action.value=false;feedback.accepted()}}
const recordsMessage=ref(''),recordsError=ref('')
async function exportIndex(){action.value=true;recordsMessage.value='';recordsError.value='';try{const path=await api().ExportIndex();if(path)recordsMessage.value=`应用记录已备份：${path}`}catch(e){recordsError.value=String(e)}finally{action.value=false}}
async function rename(name:string){await run(()=>api().RenameSource(name))}
async function inspect(item:Item){selected.value=item;mediaURL.value='';playError.value=false;history.value=[];try{const h=await api().GetHistory(item.id);if(selected.value?.id===item.id)history.value=h}catch{};if(item.format==='iso-bmff'||item.preview){try{const url=await api().GetMediaURL(item.id);if(selected.value?.id===item.id)mediaURL.value=url}catch{playError.value=true}}}
function closeDetail(){selected.value=null;mediaURL.value='';history.value=[]}
function onKey(e:KeyboardEvent){if(e.key==='Escape'){closeDetail();manager.dialog=null;if(!backupSubmitting.value)exportFilter.value=null;loadPreview.value=null}}
watch(()=>state.value.revision,()=>{failed.value=new Set()})
watch([year,kind,month,role,status,size,view,from,to,includeBaseline],()=>{offset.value=0;if(desktop.value)void load()})
watch(view,()=>{year.value='';month.value='';from.value='';to.value=''})
watch([from,to],()=>{if(view.value==='observed'&&(from.value||to.value)){month.value='';year.value=''}})
watch(includeBaseline,()=>{month.value=''})
watch(offset,()=>{void load()})
onMounted(()=>{desktop.value=hasDesktop();void poll();timer=setInterval(()=>void poll(),800);window.addEventListener('keydown',onKey)})
onUnmounted(()=>{disposed=true;if(timer)clearInterval(timer);window.removeEventListener('keydown',onKey)})
</script>

<template>
<div class="shell">
 <AppearanceBackground/>
 <aside class="sidebar">
  <div class="brand"><strong>Qman</strong><span>媒体管理</span></div>
  <label class="source-label" for="source-choice">{{state.mode==='backup'?'独立备份':'当前账号 / 来源'}}</label>
  <select id="source-choice" class="source-select" aria-label="切换媒体来源" :disabled="busy" @change="switchSource" :value="state.mode==='source'?(state.source?.id??''):''">
   <option value="" disabled>{{state.mode==='backup'?'切换回账号来源':'请选择账号 / 来源'}}</option>
   <option v-for="s in state.sources" :key="s.id" :value="s.id" :title="s.root">{{sourceLabel(s)}}</option>
  </select>
  <button class="source-choose" :disabled="!desktop||busy" @click="choose">选择 QQ 数据文件夹</button>
  <nav class="type-navigation" aria-label="媒体导航">
   <button class="nav-item" :class="{active:!settingsPage&&!historyPage&&!kind}" @click="settingsPage=false;historyPage=false;kind='';month=''">全部类型 <span v-if="state.report">{{visibleFiles.toLocaleString()}}</span></button>
   <button v-for="k in kinds" :key="k.id" class="nav-item" :class="{active:!settingsPage&&!historyPage&&kind===k.id}" @click="settingsPage=false;historyPage=false;kind=k.id;month=''">{{k.name}}</button>
   <button class="nav-item history-nav" :class="{active:!settingsPage&&historyPage}" @click="settingsPage=false;historyPage=true">备份历史</button>
  </nav>
  <template v-if="!settingsPage&&!historyPage">
   <button type="button" class="nav-label month-heading" aria-controls="sidebar-period-list" :aria-pressed="sidebarPeriod==='year'" :title="sidebarPeriod==='month'?'点击切换为年份':'点击切换为月份'" @click="sidebarPeriod=sidebarPeriod==='month'?'year':'month'">{{view==='observed'?(sidebarPeriod==='month'?'发现月份':'发现年份'):(sidebarPeriod==='month'?'来源月份':'来源年份')}}</button>
   <div id="sidebar-period-list" class="month-list" :class="{'month-mode':sidebarPeriod==='month'}">
    <template v-if="sidebarPeriod==='year'">
     <button class="month-item" :class="{active:!year}" @click="selectSidebarYear('')">全部年份</button>
     <button v-for="[y,count] in availableYears" :key="y" class="month-item" :class="{active:year===y}" @click="selectSidebarYear(y)">{{y}}<span>{{count}}</span></button>
    </template>
    <template v-else>
     <button class="month-item" :class="{active:!month}" @click="selectMonth('')">全部月份</button>
     <button v-for="[m,count] in months" :key="m" class="month-item" :class="{active:month===(m||'__unknown__')}" @click="selectMonth(m||'__unknown__')">{{m||'月份未知'}}<span>{{count}}</span></button>
    </template>
    <p v-if="sidebarPeriod==='month'?!months.length:!availableYears.length" class="sidebar-hint">{{view==='observed'&&!includeBaseline?'首次导入记录默认不计新增。':'当前范围没有可用日期。'}}</p>
   </div>
  </template>
  <button class="nav-item settings-nav" :class="{active:settingsPage}" :disabled="!desktop" @click="settingsPage=true;historyPage=false">设置</button>
  <div class="sidebar-footer">本机处理 <span>0.6.5</span></div>
 </aside>
 <main>
  <header class="topbar">
   <h1>{{settingsPage?'设置':historyPage?'备份历史':state.mode==='backup'?'独立备份 · '+title:state.source?(year&&!month?year+' 年 · ':'')+title:'媒体库'}}</h1>
   <div class="header-actions"><SourceDetails v-if="currentPage==='media'&&state.source" :source="state.source" :mode="state.mode" :busy="busy" :discovery="state.discovery" @rename="rename"/><button class="secondary" :disabled="!desktop||busy" @click="openBackup()">加载备份</button></div>
  </header>
  <div class="workspace">
   <FeedbackNotice v-for="item in feedback.visible.value" :key="item.entry.id" :feedback="item.entry" dismissible @dismiss="feedback.dismiss(item.kind)"/>
   <SettingsPage v-if="settingsPage" :busy="busy" :records-message="recordsMessage" :records-error="recordsError" @imported="importedDefault" @export-records="exportIndex"/>
   <div v-else-if="historyPage">
    <div v-if="busy" class="scan-progress" role="status">{{progressTitle}} · 已处理 {{state.scanned}} 项<button v-if="scanning" :disabled="stopping||state.stopping" @click="terminateUpdate">{{stopping||state.stopping?'正在终止…':'终止更新'}}</button><button v-else @click="cancel">取消任务</button></div>
    <BackupHistory :busy="busy" @load="loadHistory" @relocate="openBackup" @view="viewLoaded" @remove="removeBackups" @forget="forgetBackup" @check="checkBackupOriginals" @clean="manager.prepareBackups" @clean-results="manager.results"/>
   </div>
   <template v-else>
    <div v-if="!desktop" class="notice">请使用 Qman 桌面程序管理本地媒体。浏览器预览不读取本机目录。</div>
    <section v-if="!state.source" class="empty-card">
     <h2>选择 QQ 数据文件夹</h2><p>支持 Tencent Files、账号目录或 nt_data。识别账号并导入媒体清单，原文件保持原位。</p>
     <button class="primary" :disabled="!desktop||busy" @click="choose">选择 QQ 数据文件夹</button><small>导入只读，无需登录 QQ。</small>
    </section>
    <template v-else>
     <div v-if="state.discovery&&!state.discovery.complete" class="notice warning">有些文件夹未能检查，来源列表可能不完整。已找到 {{state.discovery.candidates.length}} 个来源，未扫描其他账号的媒体内容。</div>
     <LibraryStatus :state="state" :busy="busy" :stopping="stopping" @update="start" @stop="terminateUpdate" @cancel="cancel"/>
     <template v-if="state.report">
      <section class="library-panel">
       <div class="library-toolbar"><div class="tabs" aria-label="日期视图"><button :class="{active:view==='source'}" @click="view='source'">来源月份</button><button :class="{active:view==='observed'}" @click="view='observed'">新增发现日期</button></div><div v-if="state.mode==='source'" class="library-actions"><button class="secondary" :class="{chosen:manager.active}" :disabled="busy" @click="manager.toggle">{{manager.active?'完成管理':'管理'}}</button><button class="primary" :disabled="busy||manager.active" @click="prepareBackup">备份当前筛选</button></div><span v-else class="backup-label">备份视图 · {{state.excluded}} 项未纳入当前清单</span></div>
       <div class="filterbar"><select class="month-select" :aria-label="view==='source'?'来源年份选择':'发现年份选择'" v-model="year" @change="selectYear"><option value="">全部年份</option><option v-for="y in years" :key="y" :value="y">{{y}} 年</option></select><select class="month-select" :aria-label="view==='source'?'来源月份选择':'发现月份选择'" :value="month" @change="selectMonth(($event.target as HTMLSelectElement).value)"><option value="">{{view==='source'?'全部来源月份':'全部发现月份'}}</option><option v-for="[m,count] in months" :key="m" :value="m||'__unknown__'">{{m||'月份未知'}}（{{count}}）</option></select><select aria-label="来源角色" v-model="role"><option value="">全部角色</option><option v-for="(label,value) in roles" :key="value" :value="value">{{label}}</option></select><select aria-label="文件状态" v-model="status"><option value="">全部状态</option><option v-for="(label,value) in statuses" :key="value" :value="value">{{label}}</option></select><select aria-label="最小文件大小" v-model="size"><option value="">全部大小</option><option value="1048576">至少 1 MiB</option><option value="10485760">至少 10 MiB</option><option value="104857600">至少 100 MiB</option></select><div class="view-toggle"><button :class="{chosen:!listing}" @click="listing=false" aria-label="网格视图">网格</button><button :class="{chosen:listing}" @click="listing=true" aria-label="列表视图">列表</button></div></div>
       <form v-if="view==='observed'" class="datebar" @submit.prevent="queryDate"><label>首次发现日期 <input type="date" v-model="from" aria-label="开始日期"/> 至 <input type="date" v-model="to" aria-label="结束日期"/></label><button class="secondary" type="submit" :disabled="querying">查询</button><label class="baseline-toggle"><input type="checkbox" v-model="includeBaseline"/>包含首次导入记录</label><span>自动筛选，包含结束日；不是发送或下载时间。</span></form>
       <div v-if="view==='observed'" class="observation-note">{{includeBaseline?'首次导入记录的日期仅表示建立索引时间，仍标记“扫描前已存在”。':'默认排除首次导入时已有的文件，可勾选上方选项查看。'}}<span v-if="page.baseline_files&&!includeBaseline"> 当前条件另有 {{page.baseline_files.toLocaleString()}} 条首次导入记录。</span></div>
       <div v-if="manager.active" class="manage-toolbar"><span>已选 {{selectedCount.toLocaleString()}} 个{{manager.all?'（当前筛选，跨页）':''}}</span><button class="secondary" :disabled="busy||!page.items.length" @click="manager.selectPage(page.items)">{{manager.pageSelected(page.items)?'取消本页':'选择本页'}}</button><button class="secondary" :disabled="busy||!page.total" @click="manager.selectAll">选择全部匹配结果</button><button class="text-button" :disabled="busy" @click="manager.clear">清空选择</button><button class="danger-button" :disabled="busy||!selectedCount" @click="manager.prepare">删除（{{selectedCount}}）</button></div>
       <div class="result-caption"><div><strong>当前筛选 {{page.total.toLocaleString()}} 个文件</strong><span>逻辑大小 {{bytes(page.bytes)}} · 含原件、预览和临时资源，不是可释放空间</span></div><button class="text-button" @click="resetFilters">清空筛选</button></div>
       <div v-if="querying" class="query-indicator" role="status">正在更新筛选结果…</div>
       <div v-if="!page.items.length&&!querying" class="no-results"><template v-if="view==='observed'&&!includeBaseline&&page.baseline_files"><p>{{page.baseline_files.toLocaleString()}} 个文件属于首次导入记录，默认不列为后续新增。</p><button class="secondary" @click="showInitialImport">查看首次导入记录</button></template><template v-else><p>没有符合当前条件的文件。</p><button class="secondary" @click="resetFilters">清空筛选</button></template></div>
       <div v-else class="media-grid" :class="{listing}"><div v-for="item in page.items" :key="item.id" class="media-entry" :class="{selected:manager.active&&manager.isSelected(item)}"><button class="media-card" @click="manager.active?manager.select(item):inspect(item)"><div class="media-preview"><img v-if="(item.preview||item.cover_id)&&!failed.has(item.id)" :src="thumb(item)" alt="静态缩略预览" loading="lazy" @error="failPreview(item.id)"/><div v-else class="media-placeholder"><span>{{kindName(item.kind)}}</span><small>{{failed.has(item.id)?'预览失败':item.format==='iso-bmff'?'点击尝试播放':item.format==='silk'?'SILK · 原文件保留':item.status==='recognized'?'暂不支持预览':statuses[item.status]}}</small></div><span class="role-tag">{{roles[item.role]}}</span></div><div class="media-info"><strong :title="item.relative_path">{{name(item)}}</strong><div><span>{{item.source_month||'月份未知'}} · {{item.format.toUpperCase()}}</span><span>{{bytes(item.bytes)}}</span></div><small class="item-state"><span v-if="readiness(item)" class="pending">{{readiness(item)}} · </span>{{methods[item.observation_method]??'时间证据未知'}}</small></div></button><label v-if="manager.active" class="media-check"><input type="checkbox" :aria-label="'选择 '+item.relative_path" :checked="manager.isSelected(item)" :disabled="busy" @change="manager.select(item)"/></label></div></div>
       <div class="pagination"><span>每页最多 60 个文件</span><button class="secondary" :disabled="offset===0" @click="offset=Math.max(0,offset-60)">上一页</button><span>{{Math.floor(offset/60)+1}} / {{Math.max(1,Math.ceil(page.total/60))}}</span><button class="secondary" :disabled="offset+60>=page.total" @click="offset+=60">下一页</button></div>
      </section>
      <details v-if="state.report.issue_count" class="issues"><summary>扫描问题（{{state.report.issue_count}}，最多展示 1000 项）</summary><div v-for="(issue,i) in state.report.issues" :key="i"><code>{{issue.path}}</code><span>{{issues[issue.code]??issue.code}}</span></div></details>
     </template>
    </template>
    <details v-if="restoreIssues.length" class="issues"><summary>加载项目说明</summary><div v-for="(item,i) in restoreIssues" :key="i"><code>{{item.relative_path}}</code><span>{{item.status==='fallback'?'已独立保存：':'未完成：'}}{{item.error}}</span></div></details>
    <details v-if="state.excluded_items?.length" class="issues"><summary>未加载或未包含的项目（展示 {{state.excluded_items.length}} 项，最多 1000 项）</summary><div v-for="(item,i) in state.excluded_items" :key="i"><code>{{item.path}}</code><span>{{statuses[item.code]??({temporary:'临时资源',pending:'尚未稳定',unreadable_or_changed:'核对时不可读或与缓存不符',instance_changed:'索引实例已变化',still_unstable:'本次核对后仍未稳定'} as Record<string,string>)[item.code]??item.code}}</span></div></details>
    <button v-if="state.cleanup" class="secondary cleanup-result-button" :disabled="busy" @click="manager.results()">查看最近删除任务</button>
    <footer class="workspace-footer">来源月份不是发送或下载月份。备份只复制，不减少源占用。语音暂不支持播放。</footer>
   </template>
  </div>
 </main>
 <div v-if="selected" class="modal-backdrop" @click.self="closeDetail"><section class="detail-modal" role="dialog" aria-modal="true" aria-label="媒体详情"><button class="close-modal" @click="closeDetail" aria-label="关闭详情">×</button><div class="detail-preview"><video v-if="mediaURL&&selected.format==='iso-bmff'&&!playError" :src="mediaURL" :poster="selected.cover_id&&!failed.has(selected.id)?thumb(selected):undefined" controls preload="metadata" @error="playError=true"></video><img v-else-if="mediaURL&&!playError" :src="mediaURL" alt="媒体预览" @error="playError=true"/><span v-else>{{playError?'此文件当前无法预览；可能缺少对应编码支持。':'此格式暂不支持内置预览，原文件已保留。'}}</span></div><h2>{{name(selected)}}</h2><dl><dt>源相对路径</dt><dd>{{selected.relative_path}}</dd><dt>类型 / 来源角色</dt><dd>{{kindName(selected.kind)}} / {{roles[selected.role]}}</dd><dt>来源月份</dt><dd>{{selected.source_month||'未知'}}</dd><dt>逻辑大小 / 格式</dt><dd>{{bytes(selected.bytes)}} / {{selected.format}} · {{statuses[selected.status]}}</dd><dt>首次发现</dt><dd>{{fmtTime(selected.first_seen_at)}} · {{methods[selected.observation_method]??'未知'}}</dd><dt>稳定性</dt><dd>{{selected.stability==='stable_heuristic'?'通过稳定性检查（启发式）':readiness(selected)||'状态未知'}}</dd><dt>稳定观察时间</dt><dd>{{fmtTime(selected.stable_observed_at)}}</dd><dt>文件系统创建时间</dt><dd>{{fmtTime(selected.fs_created_at)}}</dd><dt>文件系统修改时间</dt><dd>{{fmtTime(selected.fs_modified_at)}}</dd></dl><details v-if="history.length" class="history"><summary>查看已保存的观察记录（{{history.length}}）</summary><p v-for="(h,i) in history" :key="i">{{fmtTime(h.at)}} · {{eventNames[h.kind]??h.kind}}</p></details><p>首次发现是本工具的观察证据，不是 QQ 下载时间；稳定和文件头识别也不证明完整下载。原件、封面和临时文件分别管理。</p></section></div>
 <MediaDeleteDialog v-if="manager.dialog" :job="manager.dialog" :busy="busy" :read-only="manager.dialogReadOnly" @close="manager.dialog=null" @confirm="manager.execute"/><BackupExportDialog v-if="exportFilter" :initial="exportFilter" :submitting="backupSubmitting" :submit-error="backupError" :revision="state.revision" @close="exportFilter=null" @confirm="backup"/>
 <BackupLoadDialog v-if="loadPreview" :preview="loadPreview" @close="loadPreview=null" @confirm="confirmLoad"/>
</div>
</template>
