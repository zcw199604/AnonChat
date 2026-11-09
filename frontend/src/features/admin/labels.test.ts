import { describe, it, expect } from 'vitest'
import {
  auditAction,
  banSubject,
  contentStatus,
  formatTime,
  reportReason,
  reportStatus,
} from './labels'

describe('labels：举报原因', () => {
  it('已知原因映射为中文与色调', () => {
    expect(reportReason('spam')).toEqual({ label: '广告', tone: 'warning' })
    expect(reportReason('harassment')).toEqual({ label: '骚扰', tone: 'danger' })
    expect(reportReason('porn')).toEqual({ label: '色情', tone: 'danger' })
    expect(reportReason('illegal')).toEqual({ label: '违法', tone: 'danger' })
    expect(reportReason('other')).toEqual({ label: '其他', tone: 'neutral' })
  })

  it('未知原因原样返回并兜底 neutral', () => {
    expect(reportReason('unknown_reason')).toEqual({ label: 'unknown_reason', tone: 'neutral' })
  })
})

describe('labels：举报状态', () => {
  it('已知状态', () => {
    expect(reportStatus('open')).toEqual({ label: '待处理', tone: 'warning' })
    expect(reportStatus('resolved')).toEqual({ label: '已处理', tone: 'success' })
  })

  it('未知状态兜底', () => {
    expect(reportStatus('weird')).toEqual({ label: 'weird', tone: 'neutral' })
  })
})

describe('labels：内容状态', () => {
  it('已知状态', () => {
    expect(contentStatus('pending')).toEqual({ label: '待审核', tone: 'warning' })
    expect(contentStatus('approved')).toEqual({ label: '已通过', tone: 'success' })
    expect(contentStatus('rejected')).toEqual({ label: '已驳回', tone: 'danger' })
    expect(contentStatus('removed')).toEqual({ label: '已下架', tone: 'neutral' })
  })

  it('未知状态兜底', () => {
    expect(contentStatus('archived')).toEqual({ label: 'archived', tone: 'neutral' })
  })
})

describe('labels：封禁主体', () => {
  it('已知主体类型', () => {
    expect(banSubject('identity')).toBe('匿名身份')
    expect(banSubject('ip')).toBe('IP')
    expect(banSubject('account')).toBe('实名账号')
  })

  it('未知主体类型原样返回', () => {
    expect(banSubject('device')).toBe('device')
  })
})

describe('labels：审计动作', () => {
  it('已知动作映射为中文', () => {
    expect(auditAction('login_success')).toBe('登录成功')
    expect(auditAction('create_ban')).toBe('创建封禁')
    expect(auditAction('approve_broadcast')).toBe('通过广播')
    expect(auditAction('remove_work')).toBe('下架作品')
    expect(auditAction('bot.settings.update')).toBe('修改 Bot 设置')
    expect(auditAction('media.folder.rename')).toBe('重命名媒体文件夹')
  })

  it('未知动作原样显示', () => {
    expect(auditAction('something.new')).toBe('something.new')
  })
})

describe('labels：时间格式化', () => {
  it('空值显示占位符', () => {
    expect(formatTime(null)).toBe('—')
    expect(formatTime(undefined)).toBe('—')
  })

  it('非法时间原样返回', () => {
    expect(formatTime('not-a-date')).toBe('not-a-date')
  })

  it('使用 zh-CN 的 24 小时制', () => {
    expect(formatTime('2024-03-05T13:04:05Z')).toMatch(/2024/)
  })
})
