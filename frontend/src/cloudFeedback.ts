import {errorFeedback,type Feedback} from './feedback'

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
