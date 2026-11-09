import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

const NOW = '2024-01-01T00:00:00Z'

// 覆盖全部后台接口，保证每个路由都能在数据返回后完成渲染。
vi.mock('../../lib/api/client', () => {
  const ok = () => Promise.resolve(undefined)
  return {
    ApiError: class ApiError extends Error {
      code = 'internal'
      status = 500
    },
    adminStats: () =>
      Promise.resolve({ online: 1, activeConversations: 2, messages24h: 3, openReports: 4 }),
    adminLogin: ok,
    adminLogout: ok,
    adminReports: () =>
      Promise.resolve({
        reports: [
          {
            id: 'r1',
            reportedIdentityId: 'x',
            reason: 'harassment',
            status: 'open',
            createdAt: NOW,
          },
        ],
        nextCursor: 'c1',
      }),
    adminReport: () =>
      Promise.resolve({
        id: 'r1',
        reportedIdentityId: 'x',
        reason: 'harassment',
        status: 'open',
        createdAt: NOW,
        snapshot: [{ id: 1, memberId: 1, nickname: '甲', body: '你好', reported: true }],
        reported: {
          nickname: '甲',
          createdIp: '1.1.1.1',
          lastIp: '1.1.1.1',
          phoneMasked: '',
          reportCount: 1,
        },
        activeBans: [],
      }),
    adminResolveReport: ok,
    adminBans: () =>
      Promise.resolve({
        bans: [
          { id: 'b1', subjectType: 'identity', subjectValue: 'v', reason: 'r', active: true, expiresAt: null },
        ],
        nextCursor: null,
      }),
    adminRevokeBan: ok,
    adminOnlineUsers: () =>
      Promise.resolve({
        users: [
          {
            identityId: 'id1',
            publicCode: 'ABCD3FGH9K',
            nickname: '甲',
            accountId: '',
            ips: ['1.1.1.1'],
            connCount: 1,
            connectedAt: NOW,
            userAgent: 'UA',
          },
        ],
        total: 1,
      }),
    adminAuditLogs: () =>
      Promise.resolve({
        logs: [
          { id: 1, adminId: 'a', action: 'login_success', target: 't', detail: null, createdAt: NOW },
        ],
        nextCursor: 2,
      }),
    adminAnnouncements: () =>
      Promise.resolve({ announcements: [{ id: 'a1', content: '公告内容', createdAt: NOW }] }),
    adminCreateAnnouncement: ok,
    adminDeleteAnnouncement: ok,
    adminBroadcasts: () =>
      Promise.resolve({
        broadcasts: [{ id: 'br1', content: '广播', status: 'pending', publicCode: 'PC', createdAt: NOW }],
      }),
    adminApproveBroadcast: ok,
    adminRejectBroadcast: ok,
    adminRemoveBroadcast: ok,
    adminActivationBatches: () =>
      Promise.resolve({
        batches: [
          {
            id: 'bat1',
            label: '批次',
            vipDays: 30,
            broadcastCards: 3,
            codeCount: 10,
            expiresAt: NOW,
            createdAt: NOW,
            total: 10,
            redeemed: 2,
            revoked: 0,
          },
        ],
      }),
    adminCreateActivationBatch: () => Promise.resolve({ batches: [], codes: ['AAA'] }),
    adminRevokeActivationBatch: () => Promise.resolve({ revoked: 3 }),
    adminWorks: () =>
      Promise.resolve({ works: [{ id: 'w1', title: '作品', status: 'pending', likeCount: 2, createdAt: NOW }] }),
    adminApproveWork: ok,
    adminRejectWork: ok,
    adminRemoveWork: ok,
    adminLookupIdentity: () =>
      Promise.resolve({
        identityId: 'id1',
        publicCode: 'PC',
        createdAt: NOW,
        vipActive: true,
        banned: false,
        summary: { nickname: '甲', createdIp: '', lastIp: '', phoneMasked: '', reportCount: 0 },
      }),
  }
})

vi.mock('./botApi', () => {
  const ok = () => Promise.resolve(undefined)
  return {
    adminBotSettings: () =>
      Promise.resolve({
        enabled: true,
        baseUrl: '',
        model: '',
        apiKeySet: false,
        apiKeyHint: '',
        apiKeySource: 'none',
        apiKeyStale: false,
        secretStorageAvailable: true,
        timeoutSeconds: 30,
        timezone: 'Asia/Shanghai',
        fallbackSeconds: 10,
        bottleMinAgeMinutes: 60,
        sendCacheKey: false,
        ready: false,
        notReadyReason: '',
      }),
    adminBots: () => Promise.resolve([]),
    adminCharacterCards: () => Promise.resolve([]),
    adminMediaFolders: () => Promise.resolve([]),
    adminMediaItems: () => Promise.resolve([]),
    adminSaveBotSettings: ok,
    adminTestBotConnection: ok,
    adminCreateBot: ok,
    adminUpdateBot: ok,
    adminDeleteBot: ok,
    adminResetBotUsage: ok,
    adminCreateCharacterCard: ok,
    adminUpdateCharacterCard: ok,
    adminDeleteCharacterCard: ok,
    adminCreateMediaFolder: ok,
    adminRenameMediaFolder: ok,
    adminDeleteMediaFolder: ok,
    adminUploadMedia: ok,
    adminUpdateMediaItem: ok,
    adminDeleteMediaItem: ok,
  }
})

import AdminPage from './AdminPage'

const PATHS = [
  '/admin',
  '/admin/reports',
  '/admin/reports/r1',
  '/admin/bans',
  '/admin/lookup',
  '/admin/lookup?code=abcd3fgh9k',
  '/admin/announcements',
  '/admin/codes',
  '/admin/broadcasts',
  '/admin/works',
  '/admin/bot',
  '/admin/audit',
]

describe('后台路由都能渲染出侧边栏与页面标题', () => {
  for (const path of PATHS) {
    it(path, async () => {
      render(
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/admin/*" element={<AdminPage />} />
          </Routes>
        </MemoryRouter>,
      )
      expect(await screen.findByRole('navigation', { name: '后台导航' })).toBeInTheDocument()
      // 每个页面都有 PageHeader 的 h1。
      expect(screen.getByRole('heading', { level: 1 })).toBeInTheDocument()
    })
  }

  it('未知路径重定向到概览', async () => {
    render(
      <MemoryRouter initialEntries={['/admin/does-not-exist']}>
        <Routes>
          <Route path="/admin/*" element={<AdminPage />} />
        </Routes>
      </MemoryRouter>,
    )
    expect(await screen.findByRole('heading', { level: 1, name: '概览' })).toBeInTheDocument()
  })
})
