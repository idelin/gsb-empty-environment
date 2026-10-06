/**
 * FocusManager — 用虚拟节点树管理弹层栈的焦点。
 *
 * 模型：
 * - 每个弹层（layer）拥有一棵虚拟节点树（VNode），节点可禁用、可删除。
 * - 弹层构成栈；开层时记录触发节点及其祖先链（fallbackChain）。
 * - 关层时焦点恢复顺序：触发节点 → 最近仍可聚焦祖先 → 父层首项。
 * - 异步（deferred）关闭通过 epoch 令牌防止旧层恢复抢走新层焦点。
 */

export interface VNode {
  id: string;
  layerId: string;
  parentId: string | null;
  disabled: boolean;
  order: number;
}

export interface Layer {
  id: string;
  triggerId: string | null;
  /** 开层瞬间捕获的触发节点祖先链（由近及远）。 */
  fallbackChain: string[];
}

export type Scheduler = (fn: () => void) => void;
export type FocusListener = (id: string | null) => void;

export class FocusManager {
  private nodes = new Map<string, VNode>();
  private layers = new Map<string, Layer>();
  private stack: string[] = [];
  private orderCounter = 0;
  /** 每次栈变更（开/关层）自增；用于判定异步恢复是否已过期。 */
  private epoch = 0;
  private listeners: FocusListener[] = [];
  private schedule: Scheduler;

  focusedId: string | null = null;
  /** 全部焦点变迁记录，供事件重放核对。 */
  readonly focusLog: (string | null)[] = [];

  constructor(schedule: Scheduler = (fn) => queueMicrotask(fn)) {
    this.schedule = schedule;
  }

  // ---- 结构维护 ----

  /** 创建基础层（栈底）。应用启动时调用一次。 */
  createBaseLayer(id: string): void {
    if (this.stack.length > 0) throw new Error(`base layer already exists`);
    this.layers.set(id, { id, triggerId: null, fallbackChain: [] });
    this.stack.push(id);
    this.epoch++;
  }

  registerNode(id: string, layerId: string, parentId: string | null = null, disabled = false): void {
    if (!this.layers.has(layerId)) throw new Error(`unknown layer: ${layerId}`);
    if (parentId !== null && !this.nodes.has(parentId)) throw new Error(`unknown parent node: ${parentId}`);
    this.nodes.set(id, { id, layerId, parentId, disabled, order: this.orderCounter++ });
    // 弹层内容晚于开层渲染：栈顶层出现首个可聚焦节点时接管焦点。
    const topLayerId = this.stack[this.stack.length - 1];
    if (!disabled && layerId === topLayerId && !this.isFocusWithin(layerId)) {
      this.setFocus(id);
    }
  }

  removeNode(id: string): void {
    const node = this.nodes.get(id);
    if (!node) return;
    this.nodes.delete(id);
    if (this.focusedId === id) {
      // 被删节点正持有焦点：退到最近可聚焦祖先，再退到本层首项。
      const target = this.nearestFocusableAncestor(node) ?? this.firstFocusable(node.layerId);
      this.setFocus(target);
    }
  }

  setDisabled(id: string, disabled: boolean): void {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    node.disabled = disabled;
    if (disabled && this.focusedId === id) {
      const target = this.nearestFocusableAncestor(node) ?? this.firstFocusable(node.layerId);
      this.setFocus(target);
    }
  }

  // ---- 弹层栈 ----

  openLayer(id: string, triggerId: string | null = null): void {
    if (this.layers.has(id)) throw new Error(`layer already open: ${id}`);
    if (triggerId !== null && !this.nodes.has(triggerId)) throw new Error(`unknown trigger node: ${triggerId}`);
    const fallbackChain: string[] = [];
    if (triggerId !== null) {
      let cursor = this.nodes.get(triggerId)!.parentId;
      while (cursor !== null) {
        fallbackChain.push(cursor);
        cursor = this.nodes.get(cursor)?.parentId ?? null;
      }
    }
    this.layers.set(id, { id, triggerId, fallbackChain });
    this.stack.push(id);
    this.epoch++;
    const first = this.firstFocusable(id);
    if (first !== null) this.setFocus(first);
  }

  /**
   * 关闭弹层。`defer: true` 表示焦点恢复异步执行（如等退场动画），
   * 期间若有任何开/关层操作，本次恢复作废，避免旧层抢走新层焦点。
   */
  closeLayer(id: string, opts: { defer?: boolean } = {}): void {
    const idx = this.stack.lastIndexOf(id);
    if (idx === -1) throw new Error(`layer not open: ${id}`);
    const wasTop = idx === this.stack.length - 1;
    const layer = this.layers.get(id)!;
    // 快照触发信息；目标在恢复时刻才解析，期间发生的删除/禁用会被尊重。
    const snapshot = { triggerId: layer.triggerId, fallbackChain: [...layer.fallbackChain] };
    this.stack.splice(idx, 1);
    this.layers.delete(id);
    this.epoch++;

    const apply = () => {
      if (!wasTop) return; // 只有关闭栈顶层才移动焦点
      this.setFocus(this.resolveRestoreTarget(snapshot));
    };
    if (opts.defer) {
      const token = this.epoch;
      this.schedule(() => {
        if (token !== this.epoch) return; // 已有更新的栈操作，恢复作废
        apply();
      });
    } else {
      apply();
    }
  }

  // ---- 查询 ----

  get stackIds(): string[] {
    return [...this.stack];
  }

  isFocusable(id: string): boolean {
    const node = this.nodes.get(id);
    return !!node && !node.disabled && this.stack.includes(node.layerId);
  }

  onFocus(listener: FocusListener): void {
    this.listeners.push(listener);
  }

  /** 程序化聚焦；目标不可聚焦时抛错。 */
  focusNode(id: string): void {
    if (!this.isFocusable(id)) throw new Error(`node not focusable: ${id}`);
    this.setFocus(id);
  }

  // ---- 内部 ----

  private resolveRestoreTarget(snapshot: { triggerId: string | null; fallbackChain: string[] }): string | null {
    if (snapshot.triggerId !== null && this.isFocusable(snapshot.triggerId)) {
      return snapshot.triggerId;
    }
    for (const ancestorId of snapshot.fallbackChain) {
      if (this.isFocusable(ancestorId)) return ancestorId;
    }
    const parentLayerId = this.stack[this.stack.length - 1];
    return parentLayerId === undefined ? null : this.firstFocusable(parentLayerId);
  }

  private isFocusWithin(layerId: string): boolean {
    return this.focusedId !== null && this.nodes.get(this.focusedId)?.layerId === layerId;
  }

  private nearestFocusableAncestor(node: VNode): string | null {
    let cursor = node.parentId;
    while (cursor !== null) {
      if (this.isFocusable(cursor)) return cursor;
      cursor = this.nodes.get(cursor)?.parentId ?? null;
    }
    return null;
  }

  private firstFocusable(layerId: string): string | null {
    let best: VNode | null = null;
    for (const node of this.nodes.values()) {
      if (node.layerId === layerId && !node.disabled && (best === null || node.order < best.order)) {
        best = node;
      }
    }
    return best === null ? null : best.id;
  }

  private setFocus(id: string | null): void {
    this.focusedId = id;
    this.focusLog.push(id);
    // 监听器可能重入开/关层；状态已落定，逐个快照调用即可。
    for (const listener of [...this.listeners]) listener(id);
  }
}
