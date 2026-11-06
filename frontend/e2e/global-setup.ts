import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const E2E_DATABASE_URL =
  process.env.E2E_DATABASE_URL ??
  'postgres://anonchat:anonchat@127.0.0.1:55432/anonchat_e2e?sslmode=disable'

export const E2E_ADMIN_USER = 'e2eadmin'
export const E2E_ADMIN_PASS = 'e2e-password-123'

/** 清空业务表（保留 goose 版本表与种子房间），并创建 E2E 管理员。 */
export default async function globalSetup(): Promise<void> {
  const here = path.dirname(fileURLToPath(import.meta.url))
  const backendDir = path.resolve(here, '../../backend')
  const env = { ...process.env, APP_ENV: 'test', DATABASE_URL: E2E_DATABASE_URL }

  // 1. 迁移到最新（幂等）。
  execFileSync('go', ['run', './cmd/server', 'migrate', 'up'], { cwd: backendDir, env, stdio: 'inherit' })

  // 2. 清空业务表。DELETE 顺序遵循外键依赖；room 会话与 rooms 保留。
  const sql = `
    DELETE FROM admin_audit_logs;
    DELETE FROM admin_sessions;
    DELETE FROM admin_users;
    DELETE FROM reports;
    DELETE FROM bans;
    DELETE FROM bottle_picks;
    DELETE FROM bottles;
    DELETE FROM private_links;
    DELETE FROM blocks;
    DELETE FROM messages;
    DELETE FROM conversation_members;
    DELETE FROM access_logs;
    DELETE FROM sms_codes;
    DELETE FROM identities;
    DELETE FROM verified_accounts;
    DELETE FROM conversations WHERE kind <> 'room';
  `
  execFileSync('psql', [E2E_DATABASE_URL, '-v', 'ON_ERROR_STOP=1', '-c', sql], { stdio: 'inherit' })

  // 3. 创建管理员（密码从 stdin 传入，仅 APP_ENV=test 允许）。
  execFileSync('go', ['run', './cmd/e2eseed', '--username', E2E_ADMIN_USER], {
    cwd: backendDir,
    env,
    input: E2E_ADMIN_PASS,
    stdio: ['pipe', 'inherit', 'inherit'],
  })
}
