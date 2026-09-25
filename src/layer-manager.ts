/**
 * LayerManager：嵌套弹层栈的焦点与隔离管理。
 *
 * 语义：
 *  - 任一时刻只有栈顶层（非退出中）弹层可交互、可持有焦点；
 *  - 打开弹层时对"背景"（层根祖先路径之外的所有子树）施加 inert 引用，
 *    按引用计数释放——关闭内层不会解除外层对背景的隔离；
 *  - 关闭即进入退出动画（exiting）：其 inert 引用立即释放、子树立即不可交互，
 *    但 DOM 由 finishExit() 才移除，退出期间可交互范围始终明确；
 *  - 焦点请求分代（epoch）提交：每次打开/关闭/重定向都推进代际，
 *    过时请求（含已入队的异步重试）不得夺焦；
 *  - 焦点恢复允许异步挂载：恢复目标未挂载时请求保持待决，
 *    树变化时重试，直到被满足或被更新的请求取代；
 *  - closeLayer / finishExit 幂等；Escape 只关闭可关闭的顶层。
 */

import { NodeTree } from './tree.ts';

export interface OpenLayerOptions {
  id: string;
  rootId: string;
  /** 关闭后优先恢复焦点的触发者 */
  triggerId?: string;
  /** 打开时的初始焦点（缺省为层内第一个可聚焦节点） */
  initialFocusId?: string;
  /** 是否允许 Escape 关闭，缺省 true */
  closable?: boolean;
}

interface Layer {
  id: string;
  rootId: string;
  triggerId: string | null;
  closable: boolean;
  /** 打开时对背景施加的 inert 引用（子树根列表） */
  claims: string[];
}

interface FocusRequest {
  epoch: number;
  reason: 'open' | 'restore' | 'retarget';
  /** 惰性求值的候选目标，按优先级排列 */
  candidates: Array<() => string | null>;
}

export interface LayerManagerDebugState {
  open: string[];
  exiting: string[];
  focused: string | null;
  epoch: number;
  pendingFocus: boolean;
  inertClaims: number;
}

export class LayerManager {
  private tree: NodeTree;
  private fallbackId: string | null;
  private stack: Layer[] = [];
  private exiting: Layer[] = [];
  private focused: string | null = null;
  private epoch = 0;
  private pending: FocusRequest | null = null;
  private queue: Array<() => void> = [];

  constructor(tree: NodeTree, options: { fallbackId?: string } = {}) {
    this.tree = tree;
    this.fallbackId = options.fallbackId ?? null;
    this.tree.onMutation(() => this.onTreeMutation());
  }

  // ---- 查询 ----

  focusedNode(): string | null {
    return this.focused;
  }

  currentEpoch(): number {
    return this.epoch;
  }

  topLayer(): { id: string; rootId: string } | null {
    const top = this.stack[this.stack.length - 1];
    return top ? { id: top.id, rootId: top.rootId } : null;
  }

  openLayerIds(): string[] {
    return this.stack.map((layer) => layer.id);
  }

  exitingLayerIds(): string[] {
    return this.exiting.map((layer) => layer.id);
  }

  hasPendingFocus(): boolean {
    return this.pending !== null;
  }

  /**
   * 可交互范围：可聚焦、不在退出中弹层内、且（栈非空时）位于顶层弹层内。
   * 退出动画仍存活时，退出层子树被明确排除。
   */
  isInteractive(id: string): boolean {
    if (!this.tree.isFocusable(id)) return false;
    for (const layer of this.exiting) {
      if (this.tree.contains(layer.rootId, id)) return false;
    }
    const top = this.stack[this.stack.length - 1];
    if (!top) return true;
    return this.tree.contains(top.rootId, id);
  }

  // ---- 操作 ----

  openLayer(options: OpenLayerOptions): void {
    if (this.findLayer(options.id)) throw new Error(`layer already exists: ${options.id}`);
    if (!this.tree.has(options.rootId)) throw new Error(`unknown layer root: ${options.rootId}`);
    const layer: Layer = {
      id: options.id,
      rootId: options.rootId,
      triggerId: options.triggerId ?? null,
      closable: options.closable ?? true,
      claims: this.computeIsolationClaims(options.rootId),
    };
    for (const claim of layer.claims) this.tree.addInertClaim(claim);
    this.stack.push(layer);
    this.commitFocus('open', [
      () => options.initialFocusId ?? null,
      () => this.tree.firstFocusableWithin(layer.rootId),
    ]);
  }

  /** 幂等：未知或已在退出中的 id 返回 false，不产生任何状态变化 */
  closeLayer(id: string): boolean {
    const index = this.stack.findIndex((layer) => layer.id === id);
    if (index === -1) return false;
    const [layer] = this.stack.splice(index, 1);
    for (const claim of layer.claims) this.tree.removeInertClaim(claim);
    this.exiting.push(layer);
    this.commitFocus('restore', [
      () => layer.triggerId,
      () => {
        const top = this.stack[this.stack.length - 1];
        return top ? this.tree.firstFocusableWithin(top.rootId) : null;
      },
      () => this.fallbackId,
    ]);
    return true;
  }

  /** 退出动画结束：移除层记录并卸载其子树。幂等。 */
  finishExit(id: string): boolean {
    const index = this.exiting.findIndex((layer) => layer.id === id);
    if (index === -1) return false;
    const [layer] = this.exiting.splice(index, 1);
    if (this.tree.has(layer.rootId)) this.tree.unmount(layer.rootId);
    return true;
  }

  /** Escape 只关闭可关闭的顶层 */
  handleKey(key: string): boolean {
    if (key !== 'Escape') return false;
    const top = this.stack[this.stack.length - 1];
    if (!top || !top.closable) return false;
    return this.closeLayer(top.id);
  }

  /** 排空异步焦点重试队列（模拟微任务/动画帧调度） */
  flush(): void {
    while (this.queue.length > 0) {
      const task = this.queue.shift();
      if (task) task();
    }
  }

  debugState(): LayerManagerDebugState {
    return {
      open: this.openLayerIds(),
      exiting: this.exitingLayerIds(),
      focused: this.focused,
      epoch: this.epoch,
      pendingFocus: this.hasPendingFocus(),
      inertClaims: this.tree.totalInertClaims(),
    };
  }

  // ---- 内部 ----

  private findLayer(id: string): Layer | undefined {
    return this.stack.find((layer) => layer.id === id) ?? this.exiting.find((layer) => layer.id === id);
  }

  /** 背景 = 层根祖先路径之外的所有兄弟子树（沿路径逐层收集） */
  private computeIsolationClaims(rootId: string): string[] {
    const claims: string[] = [];
    const onPath = new Set<string>();
    let cur: string | null = rootId;
    while (cur !== null) {
      onPath.add(cur);
      cur = this.tree.parentOf(cur);
    }
    const walk = (id: string): void => {
      for (const child of this.tree.childIdsOf(id)) {
        if (onPath.has(child)) {
          if (child !== rootId) walk(child); // 不越过层根本身：层内子树保持可交互
        }
        else claims.push(child);
      }
    };
    const root = this.tree.rootId();
    if (root !== null) walk(root);
    return claims;
  }

  private commitFocus(reason: FocusRequest['reason'], candidates: Array<() => string | null>): void {
    this.epoch += 1;
    const request: FocusRequest = { epoch: this.epoch, reason, candidates };
    this.pending = request;
    this.trySatisfy(request);
    // 异步重试：入队时捕获请求本身，执行前做分代检查，过时请求不得夺焦
    this.queue.push(() => {
      if (request.epoch !== this.epoch || this.pending !== request) return;
      this.trySatisfy(request);
    });
  }

  private trySatisfy(request: FocusRequest): void {
    if (request.epoch !== this.epoch) {
      if (this.pending === request) this.pending = null;
      return;
    }
    for (const candidate of request.candidates) {
      const id = candidate();
      if (id !== null && this.tree.has(id) && this.isInteractive(id)) {
        this.focused = id;
        if (this.pending === request) this.pending = null;
        return;
      }
    }
    // 暂不可满足：保持待决等待异步挂载；同时不让焦点滞留在非法区域
    if (this.focused !== null && !this.isInteractive(this.focused)) this.focused = null;
  }

  private onTreeMutation(): void {
    if (this.focused !== null && !this.isInteractive(this.focused) && this.pending === null) {
      // 焦点所在节点被卸载/隔离：重新定向到顶层或全局后备
      this.commitFocus('retarget', [
        () => {
          const top = this.stack[this.stack.length - 1];
          return top ? this.tree.firstFocusableWithin(top.rootId) : null;
        },
        () => this.fallbackId,
      ]);
      return;
    }
    if (this.pending !== null) this.trySatisfy(this.pending);
  }
}
