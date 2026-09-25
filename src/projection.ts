import type { MoveSpec, NodeId, Row, TreeState } from './types.ts';
import { applyMove, isValidMove, listRows } from './tree.ts';

/**
 * 统一的落点投影：鼠标（由坐标换算 gap/depth）与键盘（方向键调 gap、
 * [ / ] 调 depth）都收敛到同一个 (gap, depth) -> MoveSpec 映射，
 * 因此两种输入对同一位置产生完全相同的逻辑操作与补丁。
 *
 * gap ∈ [0, rows.length]：插入线位于 rows[gap] 之前（gap === rows.length 表示末尾）。
 * depth：期望的缩进层级。
 */
export function projectGap(rows: Row[], sourceId: NodeId, gap: number, depth: number): MoveSpec | null {
  if (rows.length === 0) return null;
  const g = Math.max(0, Math.min(gap, rows.length));
  const above = g > 0 ? rows[g - 1] : null;
  const below = g < rows.length ? rows[g] : null;
  if (!above && !below) return null;
  const d = Math.max(0, depth);
  // 深度超过上一行：成为上一行的最后一个子节点
  if (above && d > above.depth) return { sourceId, targetId: above.id, placement: 'inside' };
  // 否则作为下一行的前兄弟（或末尾时作为上一行的后兄弟）
  if (below) return { sourceId, targetId: below.id, placement: 'before' };
  return { sourceId, targetId: (above as Row).id, placement: 'after' };
}

/** 移动预览：在克隆树上投影行序列，正式树不被触碰 */
export function computePreviewRows(tree: TreeState, spec: MoveSpec): Row[] {
  if (!isValidMove(tree, spec)) return listRows(tree);
  return listRows(applyMove(tree, spec));
}
