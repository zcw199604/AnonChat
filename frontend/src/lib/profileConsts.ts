/**
 * 资料常量（how.md 3.2）。必须与后端 `backend/internal/profile/consts.json` 完全一致，
 * 前端单元测试读取同一份 JSON 做比对（profileConsts.test.ts）。
 */

/** 34 个省级行政区。 */
export const PROVINCES = [
  '北京',
  '天津',
  '河北',
  '山西',
  '内蒙古',
  '辽宁',
  '吉林',
  '黑龙江',
  '上海',
  '江苏',
  '浙江',
  '安徽',
  '福建',
  '江西',
  '山东',
  '河南',
  '湖北',
  '湖南',
  '广东',
  '广西',
  '海南',
  '重庆',
  '四川',
  '贵州',
  '云南',
  '西藏',
  '陕西',
  '甘肃',
  '青海',
  '宁夏',
  '新疆',
  '香港',
  '澳门',
  '台湾',
] as const

/** 资料标签预设（最多选 5 个）。 */
export const TAGS = [
  '音乐',
  '电影',
  '游戏',
  '动漫',
  '读书',
  '运动',
  '旅行',
  '美食',
  '摄影',
  '宠物',
  '学生党',
  '打工人',
  '夜猫子',
  '早起党',
  '情感树洞',
  '闲聊',
  '学习',
  '二次元',
] as const

/** 标签最多可选数量。 */
export const MAX_TAGS = 5

/** 年龄下限与上限。 */
export const MIN_AGE = 18
export const MAX_AGE = 80

/** 昵称长度区间。 */
export const MIN_NICKNAME = 2
export const MAX_NICKNAME = 12
