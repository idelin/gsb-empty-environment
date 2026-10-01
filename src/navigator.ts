/**
 * 焦点导航状态机。
 *
 * 流程：按键 → 先确定候选 → 候选未挂载则请求异步挂载、候选在视口外则请求异步滚动
 * → 异步完成时校验（令牌 + 布局版本）→ 候选已删除/不可见则重新规划 → 提交焦点。
 *
 * 过期规则：每次按键使令牌递增，先前慢挂载/滚动的完成回调因令牌不符被丢弃。
 * 无循环等待：重规划深度有上限，超限落到合法后备，不会无限等待。
 * 读屏同步：每次焦点提交同步追加一条公告（aria 风格），可随时校验一致性。
 */

import type { Direction } from './geometry.ts';
import { containsRect } from './geometry.ts';
import type { FocusNode, FocusTree } from './tree.ts';
import {
  collectCandidates,
  logicalSiblingStep,
  pickDirectional,
  pickWrap,
} from './candidates.ts';
import type { Scheduler } from './scheduler.ts';

export const KEY_DIRECTIONS: Record<string, Direction> = {
  ArrowUp: 'up',
  ArrowDown: 'down',
  ArrowLeft: 'left',
  ArrowRight: 'right',
};

type PendingKind = 'mount' | 'scroll';

interface PendingNav {
  token: number;
  dir: Direction;
  candidateId: string;
  /** 请求发出时的布局版本；完成时必须一致，否则重规划。 */
  layoutVersion: number;
  kind: PendingKind;
  scrollerId?: string;
}

interface ScrollWaiter {
  tokens: number[];
  offset: { x: number; y: number };
}

export class FocusNavigator {
  readonly tree: FocusTree;
  readonly scheduler: Scheduler;
  focusId: string;
  /** 导航令牌：每次按键 +1，使先前异步结果过期。 */
  token = 0;
  pending: PendingNav | null = null;
  /** 读屏公告日志：每条为 `${nodeId}:${label}`。 */
  announcements: string[] = [];
  /** 决策轨迹，供样例回放与测试断言。 */
  trace: string[] = [];
  /** 单次按键内允许的最大重规划次数（防循环等待）。 */
  maxReplans = 64;
  private replanCount = 0;
  private mountWaiters = new Map<string, number[]>();
  private scrollWaiters = new Map<string, ScrollWaiter>();

  constructor(tree: FocusTree, scheduler: Scheduler, initialFocusId: string) {
    this.tree = tree;
    this.scheduler = scheduler;
    const initial = tree.get(initialFocusId);
    if (!initial.focusable || !tree.isEffectivelyVisible(initial)) {
      throw new Error(`initial focus ${initialFocusId} is not a visible focusable node`);
    }
    this.focusId = initialFocusId;
    this.announcements.push(`${initial.id}:${initial.label}`);
    tree.onStructureChange = (removedOrder) => this.rescueIfNeeded(removedOrder);
  }

  // ---------- 键盘入口 ----------

  pressKeyEvent(key: string): void {
    const dir = KEY_DIRECTIONS[key];
    if (dir) this.pressKey(dir);
  }

  pressKey(dir: Direction): void {
    this.token++;
    this.replanCount = 0;
    this.pending = null; // 旧 pending 由令牌校验自然过期
    this.trace.push(`key:${dir} token=${this.token}`);
    this.planAndExecute(dir);
  }

  /** 模拟指针直接聚焦（如点击打开模态的按钮）。 */
  focusById(id: string): void {
    const node = this.tree.get(id);
    if (!node.focusable || !this.tree.isEffectivelyVisible(node)) {
      throw new Error(`cannot focus ${id}: not a visible focusable node`);
    }
    this.token++;
    this.pending = null;
    this.commit(node, 'pointer');
  }

  // ---------- 规划 ----------

  private planAndExecute(dir: Direction): void {
    const from = this.tree.get(this.focusId);

    // 方向布局：容器声明了 orientation 且按键沿其主轴时，按逻辑次序在兄弟间移动。
    const sibling = logicalSiblingStep(this.tree, from, dir);
    if (sibling) {
      this.executeCandidate(sibling, dir, 'logical');
      return;
    }

    const scope = this.tree.modalScopeOf(from) ?? this.tree.root;
    const candidates = collectCandidates(this.tree, scope, from);
    let pick = pickDirectional(this.tree, from, candidates, dir);
    if (!pick) {
      pick = pickWrap(this.tree, from, candidates, dir);
      if (pick) this.trace.push(`wrap -> ${pick.node.id}`);
    }
    if (!pick) {
      this.trace.push('no-candidate: focus stays');
      return;
    }
    this.executeCandidate(pick.node, dir, 'spatial');
  }

  private executeCandidate(node: FocusNode, dir: Direction, how: string): void {
    if (!node.mounted) {
      this.requestMount(node, dir);
      return;
    }
    const scroller = this.findScrollerNeedingScroll(node);
    if (scroller) {
      this.requestScroll(scroller, node, dir);
      return;
    }
    this.commit(node, `${how}:${dir}`);
  }

  /** 最近的、使候选未完整落入视口的滚动容器（嵌套时先内后外，逐层解决）。 */
  private findScrollerNeedingScroll(node: FocusNode): FocusNode | null {
    const rect = this.tree.effectiveRect(node);
    for (let p: FocusNode | null = node.parent; p; p = p.parent) {
      if (p.scroll && !containsRect(p.scroll.viewport, rect)) return p;
    }
    return null;
  }

  // ---------- 异步挂载 ----------

  private requestMount(node: FocusNode, dir: Direction): void {
    const myToken = this.token;
    this.pending = {
      token: myToken,
      dir,
      candidateId: node.id,
      layoutVersion: this.tree.layoutVersion,
      kind: 'mount',
    };
    this.trace.push(
      `mount-request ${node.id} v=${this.tree.layoutVersion} token=${myToken}`,
    );
    const existing = this.mountWaiters.get(node.id);
    if (existing) {
      existing.push(myToken); // 挂载进行中：合并等待，后按令牌裁决
      return;
    }
    this.mountWaiters.set(node.id, [myToken]);
    this.scheduler.setTimeout(() => {
      const tokens = this.mountWaiters.get(node.id) ?? [];
      this.mountWaiters.delete(node.id);
      for (const t of tokens) this.settleMount(node.id, t);
    }, node.mountDelay);
  }

  private settleMount(nodeId: string, token: number): void {
    const p = this.pending;
    if (!p || p.token !== token || p.kind !== 'mount' || p.candidateId !== nodeId) {
      this.trace.push(`mount-settled ${nodeId} token=${token} STALE (dropped)`);
      return;
    }
    if (this.tree.layoutVersion !== p.layoutVersion) {
      this.trace.push(`mount-settled ${nodeId} layout-changed v=${this.tree.layoutVersion} != ${p.layoutVersion}; replan`);
      this.replan(p.dir);
      return;
    }
    if (this.tree.has(nodeId)) this.tree.setMounted(nodeId, true);
    if (!this.tree.has(nodeId)) {
      this.trace.push(`mount-settled ${nodeId} candidate-deleted; replan`);
      this.replan(p.dir);
      return;
    }
    const node = this.tree.get(nodeId);
    if (!this.tree.isEffectivelyVisible(node)) {
      this.trace.push(`mount-settled ${nodeId} not-visible; replan`);
      this.replan(p.dir);
      return;
    }
    this.pending = null;
    // 挂载后可能仍在视口外，继续走滚动/提交。
    this.executeCandidate(node, p.dir, 'mounted');
  }

  // ---------- 异步滚动 ----------

  private requestScroll(scroller: FocusNode, target: FocusNode, dir: Direction): void {
    const myToken = this.token;
    const offset = this.computeScrollOffset(scroller, target);
    this.pending = {
      token: myToken,
      dir,
      candidateId: target.id,
      layoutVersion: this.tree.layoutVersion,
      kind: 'scroll',
      scrollerId: scroller.id,
    };
    this.trace.push(
      `scroll-request ${scroller.id} for ${target.id} v=${this.tree.layoutVersion} token=${myToken}`,
    );
    const existing = this.scrollWaiters.get(scroller.id);
    if (existing) {
      existing.tokens.push(myToken);
      existing.offset = offset; // 最新请求的目标偏移生效
      return;
    }
    this.scrollWaiters.set(scroller.id, { tokens: [myToken], offset });
    const delay = scroller.scroll ? scroller.scroll.scrollDelay : 0;
    this.scheduler.setTimeout(() => {
      const waiter = this.scrollWaiters.get(scroller.id);
      this.scrollWaiters.delete(scroller.id);
      if (!waiter) return;
      for (const t of waiter.tokens) this.settleScroll(scroller.id, t, waiter.offset);
    }, delay);
  }

  private settleScroll(scrollerId: string, token: number, offset: { x: number; y: number }): void {
    const p = this.pending;
    if (!p || p.token !== token || p.kind !== 'scroll' || p.scrollerId !== scrollerId) {
      this.trace.push(`scroll-settled ${scrollerId} token=${token} STALE (dropped)`);
      return;
    }
    if (this.tree.layoutVersion !== p.layoutVersion) {
      this.trace.push(`scroll-settled ${scrollerId} layout-changed v=${this.tree.layoutVersion} != ${p.layoutVersion}; replan`);
      this.replan(p.dir);
      return;
    }
    this.tree.applyScroll(scrollerId, offset); // 几何改变：版本 +1
    if (!this.tree.has(p.candidateId)) {
      this.trace.push(`scroll-settled ${scrollerId} candidate-deleted; replan`);
      this.replan(p.dir);
      return;
    }
    const candidate = this.tree.get(p.candidateId);
    if (!this.tree.isEffectivelyVisible(candidate)) {
      this.trace.push(`scroll-settled ${scrollerId} candidate-not-visible; replan`);
      this.replan(p.dir);
      return;
    }
    this.pending = null;
    // 嵌套场景：可能还需外层滚动，继续推进。
    this.executeCandidate(candidate, p.dir, 'scrolled');
  }

  /** 使目标矩形完整进入视口所需的最小偏移（基于当前有效几何计算）。 */
  private computeScrollOffset(scroller: FocusNode, target: FocusNode): { x: number; y: number } {
    const scroll = scroller.scroll;
    if (!scroll) throw new Error(`${scroller.id} is not a scroll container`);
    const rect = this.tree.effectiveRect(target);
    const vp = scroll.viewport;
    let { x, y } = scroll.offset;
    if (rect.x < vp.x) x -= vp.x - rect.x;
    else if (rect.x + rect.w > vp.x + vp.w) x += rect.x + rect.w - (vp.x + vp.w);
    if (rect.y < vp.y) y -= vp.y - rect.y;
    else if (rect.y + rect.h > vp.y + vp.h) y += rect.y + rect.h - (vp.y + vp.h);
    return { x, y };
  }

  // ---------- 重规划与后备 ----------

  private replan(dir: Direction): void {
    if (++this.replanCount > this.maxReplans) {
      this.trace.push('replan-overflow: legal fallback (no circular wait)');
      this.pending = null;
      const scope = this.tree.modalScopeOf(this.tree.get(this.focusId)) ?? this.tree.root;
      const fallback = this.firstFocusableIn(scope);
      if (fallback && fallback.id !== this.focusId) this.commit(fallback, 'fallback');
      return;
    }
    this.planAndExecute(dir);
  }

  /** 结构变更后的焦点救援：焦点被删或变为不可见时落到合法后备。 */
  private rescueIfNeeded(removedOrder: number | null): void {
    if (this.tree.has(this.focusId)) {
      const focus = this.tree.get(this.focusId);
      if (this.tree.isEffectivelyVisible(focus)) return;
    }
    const anchor =
      removedOrder ??
      (this.tree.has(this.focusId) ? this.tree.get(this.focusId).order : Number.MAX_SAFE_INTEGER);
    const fallback = this.legalFallback(anchor);
    this.token++;
    this.pending = null;
    if (fallback) {
      this.commit(fallback, 'rescue');
    } else {
      this.trace.push('rescue: no legal fallback available');
    }
  }

  /**
   * 合法后备：作用域（模态优先）内，逻辑次序在锚点之后的最近可聚焦节点；
   * 无则取锚点之前最近者；再无则取作用域内第一个。
   */
  private legalFallback(anchorOrder: number): FocusNode | null {
    const modal = this.topmostVisibleModal();
    const scope = modal ?? this.tree.root;
    const pool: FocusNode[] = [];
    for (const node of this.tree.walk(scope)) {
      if (node.focusable && this.tree.isEffectivelyVisible(node)) pool.push(node);
    }
    if (pool.length === 0) return null;
    const after = pool.filter((n) => n.order > anchorOrder).sort((a, b) => a.order - b.order)[0];
    if (after) return after;
    const before = pool.filter((n) => n.order < anchorOrder).sort((a, b) => b.order - a.order)[0];
    return before ?? pool[0];
  }

  private topmostVisibleModal(): FocusNode | null {
    let found: FocusNode | null = null;
    for (const node of this.tree.walk()) {
      if (node.modal && this.tree.isChainVisible(node)) found = node;
    }
    return found;
  }

  private firstFocusableIn(scope: FocusNode): FocusNode | null {
    for (const node of this.tree.walk(scope)) {
      if (node.focusable && this.tree.isEffectivelyVisible(node)) return node;
    }
    return null;
  }

  // ---------- 提交与校验 ----------

  private commit(node: FocusNode, reason: string): void {
    // 绝不把焦点交给不可见/未挂载的旧节点。
    if (!this.tree.isEffectivelyVisible(node)) {
      this.trace.push(`commit-blocked ${node.id}: not visible`);
      return;
    }
    this.focusId = node.id;
    this.pending = null;
    this.announcements.push(`${node.id}:${node.label}`);
    this.trace.push(`focus -> ${node.id} (${reason})`);
  }

  /** 不变量校验：返回违例列表（空数组 = 正常）。 */
  checkInvariants(): string[] {
    const violations: string[] = [];
    if (!this.tree.has(this.focusId)) {
      violations.push(`focus-lost: ${this.focusId} no longer exists`);
      return violations;
    }
    const focus = this.tree.get(this.focusId);
    if (!this.tree.isEffectivelyVisible(focus)) {
      violations.push(`focus-stale: ${this.focusId} is not effectively visible`);
    }
    const last = this.announcements[this.announcements.length - 1];
    if (!last || !last.startsWith(`${this.focusId}:`)) {
      violations.push(`sr-out-of-sync: last announcement "${last}" != focus ${this.focusId}`);
    }
    return violations;
  }
}
