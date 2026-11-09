import { useAdmin } from '../context'
import { BotPanel } from '../BotPanel'
import { PageHeader } from '../ui'

/** Bot 配置页：外层只加页头，面板内部逻辑保持不变。 */
export function BotConfigPage() {
  const { onError } = useAdmin()
  return (
    <div>
      <PageHeader
        title="Bot 配置"
        description="配置模型接入、bot 列表、角色卡与媒体库；改动即时对匹配会话生效。"
      />
      <BotPanel onError={onError} />
    </div>
  )
}
