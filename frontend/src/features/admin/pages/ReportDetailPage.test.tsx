import {render,screen} from '@testing-library/react'
import {expect,it,vi} from 'vitest'
import {MemoryRouter,Route,Routes} from 'react-router-dom'
import {ReportDetailPage} from './ReportDetailPage'
vi.mock('../../../lib/api/client',async original=>({...await original<typeof import('../../../lib/api/client')>(),adminReport:vi.fn().mockResolvedValue({id:'r1',reason:'spam',status:'resolved',createdAt:'2026-11-13T00:00:00Z',reported:{nickname:'被举报者',reportCount:1},activeBans:[],snapshot:[{id:1,nickname:'甲',body:'图片配文',kind:'image',mediaId:'image-id'},{id:2,nickname:'乙',body:'视频配文',kind:'video',mediaId:'video-id'},{id:3,nickname:'丙',body:'原文本',kind:'text'},{id:4,nickname:'丁',body:'媒体已删除',kind:'image',mediaId:null}]})}))
it('证据快照图片视频使用管理员媒体接口，保留配文和普通文本',async()=>{
 const {container}=render(<MemoryRouter initialEntries={['/admin/reports/r1']}><Routes><Route path="/admin/reports/:id" element={<ReportDetailPage/>}/></Routes></MemoryRouter>)
 expect(await screen.findByText('图片配文')).toBeInTheDocument()
 expect(screen.getByAltText('举报图片')).toHaveAttribute('src','/api/admin/media-items/image-id/file')
 expect(container.querySelector('video')).toHaveAttribute('src','/api/admin/media-items/video-id/file')
 expect(container.querySelector('video')).toHaveAttribute('controls')
 expect(screen.getByText('视频配文')).toBeInTheDocument()
 expect(screen.getByText('原文本')).toBeInTheDocument()
 expect(screen.getByText('媒体已删除')).toBeInTheDocument()
})
