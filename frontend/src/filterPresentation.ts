import type {Filter} from './api'

const mediaTypes:Record<string,string>={image:'图片',video:'视频',emoji:'收到的表情',voice:'语音'}
const recordEndpoint=(value:string)=>value?value.includes('T')?value.replace('T',' ')+' 时':value:'不限'

export function filterScope(f:Filter){
 const parts=[mediaTypes[f.kind]||'全部类型']
 if(f.view==='observed')parts.push('首次记录时间')
 if(f.month)parts.push(f.month==='__unknown__'?'月份未知':f.month)
 else if(f.month_from||f.month_to)parts.push(`${f.month_from||'不限'} 至 ${f.month_to||'不限'}`)
 else if(!f.from&&!f.to)parts.push('全部月份')
 if(f.from||f.to)parts.push(`${recordEndpoint(f.from)} 至 ${recordEndpoint(f.to)}`)
 if(f.source_month)parts.push(f.source_month==='__unknown__'?'来源月份未知':`来源 ${f.source_month}`)
 return parts.join(' · ')
}
