import {errorFeedback,type Feedback} from './feedback'

export const cloudUploadLabels:Record<string,string>={
 preparing:'核对备份',packing:'正在打包',checking:'检查文件',hashing:'计算文件摘要',initializing:'准备上传',
 authorizing:'获取上传地址',uploading:'上传中',completing:'完成登记',verifying:'核对结果',
 complete:'上传完成',failed:'上传未完成',cancelled:'已取消'
}

const activeUploadPhases=new Set(['preparing','packing','checking','hashing','initializing','authorizing','uploading','completing','verifying'])
export function isCloudUploadActive(phase:string){return activeUploadPhases.has(phase)}

export function cloudErrorFeedback(value:unknown):Feedback {
 const result=errorFeedback(value),text=String(value).replace(/^Error:\s*/,'')
 if(/无痕|滑块|人机|安全验证|图形验证/.test(text))return {...result,title:'需要额外安全验证',advice:'请切换扫码登录。'}
 if(/账号.*密码|密码.*错误|密码不正确|账号.*不存在|用户名.*错误/.test(text))return {...result,title:'账号或密码未通过验证',advice:'请检查账号和密码。'}
 if(/登录.*失效|未登录|重新登录/.test(text))return {...result,title:'云盘登录已失效',advice:'请重新登录后再试。'}
 if(/同名|已存在/.test(text))return {...result,title:'云端已有同名对象',advice:'请使用不同名称。'}
 if(/私有数据/.test(text))return {...result,title:'不能上传应用私有数据',advice:'请选择普通测试文件。'}
 if(/空间不足|容量不足|流量超限|额度不足|quota/i.test(text))return {...result,title:'云盘容量或额度不足',advice:'请检查云盘账号的可用额度。'}
 if(result.title==='这次操作没有完成')return {...result,title:'云盘操作未完成',advice:''}
 return result
}
