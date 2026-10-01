/**
 * 方向候选选择。
 *
 * 公开主轴优先规则（primary-axis-first）：
 *   1. 候选中心在主轴上的位移必须严格为正（沿按键方向）；
 *   2. 先按主轴位移升序，再按副轴偏移升序，最后按逻辑次序升序；
 *   3. 斜向候选（两轴均有位移）不另设规则，同样按上述次序参与竞争。
 *
 * 回环规则：主轴正方向无候选时，改取主轴坐标最靠反方向极值的候选，
 * 平局按副轴偏移、逻辑次序裁决。
 */

import type { Direction } from './geometry.ts';
import { primaryDelta, orthogonalDelta } from './geometry.ts';
import type { FocusNode, FocusTree } from './tree.ts';

export interface Scored {
  node: FocusNode;
  primary: number;
  orthogonal: number;
}

/** 收集作用域内的合法候选：有效可见（允许未挂载）、可聚焦、排除自身及其祖先。 */
export function collectCandidates(tree: FocusTree, scope: FocusNode, from: FocusNode): FocusNode[] {
  const out: FocusNode[] = [];
  for (const node of tree.walk(scope)) {
    if (!node.focusable) continue;
    // 排除自身与祖先：焦点已在祖先内部时，祖先不应再成为空间候选。
    let skip = false;
    for (let p: FocusNode | null = from; p; p = p.parent) {
      if (p === node) { skip = true; break; }
    }
    if (skip) continue;
    if (tree.isEffectivelyVisible(node, /* allowUnmounted */ true)) out.push(node);
  }
  return out;
}

function compare(a: Scored, b: Scored): number {
  return a.primary - b.primary || a.orthogonal - b.orthogonal || a.node.order - b.node.order;
}

/** 主轴优先选取；无候选时返回 null（由调用方进入回环）。 */
export function pickDirectional(
  tree: FocusTree,
  from: FocusNode,
  candidates: FocusNode[],
  dir: Direction,
): Scored | null {
  const fromRect = tree.effectiveRect(from);
  const scored: Scored[] = [];
  for (const node of candidates) {
    const rect = tree.effectiveRect(node);
    const primary = primaryDelta(fromRect, rect, dir);
    if (primary <= 0) continue;
    scored.push({ node, primary, orthogonal: orthogonalDelta(fromRect, rect, dir) });
  }
  scored.sort(compare);
  return scored[0] ?? null;
}

/** 回环选取：取主轴反方向极值处的候选。 */
export function pickWrap(
  tree: FocusTree,
  from: FocusNode,
  candidates: FocusNode[],
  dir: Direction,
): Scored | null {
  const fromRect = tree.effectiveRect(from);
  let best: Scored | null = null;
  for (const node of candidates) {
    const rect = tree.effectiveRect(node);
    // 回环距离：沿主轴取“反向位移”，最大者即反方向极值。
    const primary = -primaryDelta(fromRect, rect, dir);
    const entry: Scored = { node, primary, orthogonal: orthogonalDelta(fromRect, rect, dir) };
    // 反向位移最大者即反方向极值；平局按副轴偏移、逻辑次序裁决。
    if (
      !best ||
      entry.primary > best.primary ||
      (entry.primary === best.primary &&
        (entry.orthogonal < best.orthogonal ||
          (entry.orthogonal === best.orthogonal && entry.node.order < best.node.order)))
    ) {
      best = entry;
    }
  }
  return best;
}

/**
 * 方向布局：当前节点处于带 orientation 的容器且按键沿其主轴时，
 * 按逻辑次序在兄弟间移动（含回环），优先于几何选取。
 */
export function logicalSiblingStep(
  tree: FocusTree,
  from: FocusNode,
  dir: Direction,
): FocusNode | null {
  const parent = from.parent;
  if (!parent || !parent.orientation) return null;
  const along =
    (parent.orientation === 'horizontal' && (dir === 'left' || dir === 'right')) ||
    (parent.orientation === 'vertical' && (dir === 'up' || dir === 'down'));
  if (!along) return null;
  const siblings = parent.children.filter(
    (c) => c.focusable && tree.isEffectivelyVisible(c, /* allowUnmounted */ true),
  );
  if (siblings.length === 0) return null;
  const index = siblings.indexOf(from);
  if (index < 0) return null;
  const delta = dir === 'right' || dir === 'down' ? 1 : -1;
  return siblings[(index + delta + siblings.length) % siblings.length];
}
