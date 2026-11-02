import {ref} from 'vue'
import {api,type AppearanceSettings} from './api'

export const defaultAppearance=():AppearanceSettings=>({theme:'light',color:'mist',image_id:'',image_name:'',fit:'cover',panel_opacity:100})
export const appearance=ref(defaultAppearance())
export const savedAppearance=ref(defaultAppearance())
export const appearanceError=ref('')
export const backgroundError=ref('')
export const backgroundRevision=ref(0)
export const palettes={
 light:[{id:'mist',name:'雾蓝',color:'#f7f9fc',sidebar:'#eaf2f8',soft:'#eff5fa',line:'#d8e2ec'},
        {id:'gray',name:'灰白',color:'#f4f5f7',sidebar:'#e9edf1',soft:'#eef1f4',line:'#d6dde5'},
        {id:'violet',name:'浅紫灰',color:'#f4f2f7',sidebar:'#eae5f0',soft:'#efebf5',line:'#ded7e7'}],
 dark:[{id:'slate',name:'深灰蓝',color:'#1c2430',sidebar:'#232e3d',soft:'#2b394b',line:'#4a5b70'},
       {id:'charcoal',name:'炭灰',color:'#202124',sidebar:'#292b30',soft:'#32353c',line:'#555b65'},
       {id:'violet',name:'深紫灰',color:'#26232d',sidebar:'#302b39',soft:'#3a3344',line:'#62566e'}]
}
const rgb=(hex:string)=>`${parseInt(hex.slice(1,3),16)} ${parseInt(hex.slice(3,5),16)} ${parseInt(hex.slice(5,7),16)}`
let loading:Promise<void>|undefined

export function applyAppearance(value:AppearanceSettings){
 const dark=value.theme==='dark', palette=palettes[value.theme].find(p=>p.id===value.color)??palettes[value.theme][0]
 const root=document.documentElement, alpha=value.panel_opacity/100
 const surface=dark?palette.sidebar:'#ffffff'
 const tokens:Record<string,string>={
  page:palette.color,workspace:`rgb(${rgb(palette.color)} / ${alpha})`,sidebar:`rgb(${rgb(palette.sidebar)} / ${alpha})`,surface,
  panel:`rgb(${rgb(surface)} / ${alpha})`,'panel-soft':`rgb(${rgb(palette.soft)} / ${alpha})`,
  text:dark?'#e5eaf1':'#243648',muted:dark?'#b0bdce':'#5d7083',line:palette.line,soft:palette.soft,
  accent:dark?'#93c7f0':'#326b96','accent-hover':dark?'#b7ddfa':'#25567c',
  'accent-text':dark?'#b3d8f5':'#305d7d','on-accent':dark?'#142333':'#ffffff',
  selected:dark?'#354d65':'#d5e7f5',hover:dark?'#344255':'#dfeaf4',
  preview:dark?'#18212d':'#edf2f7',warning:dark?'#f0ca8f':'#835b28',
  'warning-bg':dark?'#423526':'#fff4e7','warning-line':dark?'#846d49':'#ead8bb',
  danger:dark?'#f2aaa4':'#a13f3f','danger-bg':dark?'#452d30':'#fff1ee',
  'danger-line':dark?'#926268':'#ebcac4',backdrop:dark?'#080d16aa':'#1e304a70',
 }
 for(const [key,v] of Object.entries(tokens))root.style.setProperty(`--${key}`,v)
 root.dataset.theme=value.theme;root.dataset.color=value.color;root.style.colorScheme=value.theme
 if(appearance.value.image_id!==value.image_id)backgroundError.value=''
 appearance.value={...value}
}

export function loadAppearance(){
 return loading??=(async()=>{
  try{const value=await api().GetAppearance();savedAppearance.value={...value};applyAppearance(value);appearanceError.value=''}
  catch(e){appearanceError.value=String(e)}
 })()
}

export async function retryAppearance(){loading=undefined;await loadAppearance()}
export function commitAppearance(value:AppearanceSettings){savedAppearance.value={...value};appearanceError.value='';applyAppearance(value)}
export function revertAppearance(){applyAppearance(savedAppearance.value)}
export function refreshBackground(){backgroundError.value='';backgroundRevision.value++}
