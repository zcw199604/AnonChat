import { act,fireEvent,render,screen,waitFor } from '@testing-library/react'
import { afterEach,beforeEach,expect,it,vi } from 'vitest'
import { ExternalPage } from './ExternalPage'
import {adminExternalSettings,adminExternalStatus,adminSaveExternalSettings} from '../externalApi'
vi.mock('../externalApi',()=>({adminExternalSettings:vi.fn(),adminExternalStatus:vi.fn(),adminSaveExternalSettings:vi.fn()}))
const settings={enabled:false,chat2019Enabled:false,wanfengEnabled:false,onlineThreshold:5,localWaitSeconds:3,externalMatchSeconds:10,chat2019MaxSessions:10,wanfengMaxSessions:10}
beforeEach(()=>{
 vi.mocked(adminExternalSettings).mockResolvedValue(settings)
 vi.mocked(adminSaveExternalSettings).mockImplementation(async s=>s)
 vi.mocked(adminExternalStatus).mockResolvedValue({online:2,queue:1,providers:[{name:'wanfeng',enabled:false,available:false,reason:'未配置加密密钥',links:0,matching:0,inRoom:0,idle:0,cooldownUntil:'0001-01-01T00:00:00Z',lastError:'',lastErrorAt:'0001-01-01T00:00:00Z',counters:{started:2,found:1}}]})
})
afterEach(()=>{vi.useRealTimers();vi.clearAllMocks()})
it('加载设置和状态、校验后全量保存并解释热关闭与bot时机',async()=>{
 render(<ExternalPage/> )
 expect(await screen.findByLabelText('在线人数阈值')).toHaveValue(5)
 expect(screen.getByText('未配置加密密钥')).toBeInTheDocument()
 expect(screen.getByText('关闭总开关会立即结束所有进行中的外站会话')).toBeInTheDocument()
 fireEvent.change(screen.getByLabelText('外站匹配时长（秒）'),{target:{value:'2'}})
 fireEvent.click(screen.getByRole('button',{name:'保存设置'}))
 expect(screen.getByRole('alert')).toHaveTextContent('3–120')
 expect(adminSaveExternalSettings).not.toHaveBeenCalled()
 fireEvent.change(screen.getByLabelText('外站匹配时长（秒）'),{target:{value:'20'}})
 fireEvent.click(screen.getByLabelText('总开关'))
 fireEvent.click(screen.getByRole('button',{name:'保存设置'}))
 await waitFor(()=>expect(adminSaveExternalSettings).toHaveBeenCalledWith({...settings,enabled:true,externalMatchSeconds:20}))
})
it('状态每10秒刷新，刷新不覆盖未保存设置，卸载停止刷新',async()=>{
 vi.useFakeTimers()
 const {unmount}=render(<ExternalPage/>)
 await act(async()=>{await Promise.resolve()})
 fireEvent.change(screen.getByLabelText('在线人数阈值'),{target:{value:'9'}})
 await act(async()=>{await vi.advanceTimersByTimeAsync(10000)})
 expect(adminExternalStatus).toHaveBeenCalledTimes(2)
 expect(screen.getByLabelText('在线人数阈值')).toHaveValue(9)
 unmount()
 await act(async()=>{await vi.advanceTimersByTimeAsync(10000)})
 expect(adminExternalStatus).toHaveBeenCalledTimes(2)
})
