/**
 * 公告跑马灯是否需要滚动（how.md 1.4）：
 * 只有一条公告且放得下时不滚动，其余情况匀速滚动。
 * 容器未测量（宽度为 0）时不滚动，避免首帧闪烁。
 */
export function marqueeNeedsScroll(
  itemCount: number,
  contentWidth: number,
  containerWidth: number,
): boolean {
  if (containerWidth <= 0 || contentWidth <= 0) return false
  if (itemCount <= 1 && contentWidth <= containerWidth) return false
  return contentWidth > containerWidth
}
