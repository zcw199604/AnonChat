import { expect,it } from 'vitest'
import { EXTERNAL_FIELDS,externalToForm,externalPayload,validateExternalForm } from './externalForm'
const settings={enabled:false,chat2019Enabled:false,wanfengEnabled:false,onlineThreshold:5,localWaitSeconds:3,externalMatchSeconds:10,chat2019MaxSessions:10,wanfengMaxSessions:10}
it('设置完整转换并接受约束边界',()=>{
 const f=externalToForm(settings)
 expect(externalPayload(f)).toEqual(settings)
 expect(validateExternalForm(f)).toEqual([])
 for(const field of EXTERNAL_FIELDS)for(const bound of [field.min,field.max])expect(validateExternalForm({...f,[field.key]:String(bound)})).toEqual([])
})
it('拒绝空白、小数、非数值和范围外数字，避免空白变成0',()=>{
 const f=externalToForm(settings)
 for(const field of EXTERNAL_FIELDS)for(const value of ['', ' ', 'abc', '1.5',String(field.min-1),String(field.max+1)])expect(validateExternalForm({...f,[field.key]:value}),`${field.key}=${value}`).not.toEqual([])
})
