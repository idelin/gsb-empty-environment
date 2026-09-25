import type { NodeId, NodeSpec, NodeTree } from "./tree.ts";
import { InertRegistry } from "./inert.ts";
import { FocusManager } from "./focus.ts";
import { hitTest, type Rect } from "./layout.ts";
import type { Scheduler } from "./scheduler.ts";

export interface LayerDescriptor {
  /** 弹层标识(关闭/收尾时使用)。 */
  id: string;
  /** 弹层内容子树,打开时挂载到根之下。 */
  content: NodeSpec;
  /** 触发者节点 id;关闭后优先把焦点还给它。 */
  triggerId: NodeId | null;
  /** 是否允许 Escape 关闭(默认 true)。 */
  dismissible?: boolean;
  /** 是否有退出动画:有则需显式 finalize 才真正卸载。 */
  exitAnimation?: boolean;
  /** 初始焦点目标;缺省取范围内 autofocus 或首个可聚焦节点。 */
  initialFocusId?: NodeId;
}

type LayerPhase = "active" | "closing";

interface Layer {
  id: string;
  rootId: NodeId;
  triggerId: NodeId | null;
  dismissible: boolean;
  exitAnimation: boolean;
  phase: LayerPhase;
  /** 本层对其他区域持有的 inert 引用。 */
  holds: number[];
  /** 退出动画期间对自身子树的 inert 引用。 */
  selfHold: number | null;
}

interface PendingRestore {
  gen: number;
  preferredId: NodeId | null;
}

/**
 * 嵌套弹层管理器。
 *
 * - 任意时刻只有栈顶层拥有焦点与可交互范围;
 * - 背景隔离用 inert 引用计数实现,内层关闭不解除外层隔离;
 * - inert 引用与焦点请求按代(generation)提交,栈拓扑每次变化代号 +1,
 *   过时的异步焦点请求在执行时被丢弃,不得夺焦;
 * - 退出动画期间层进入 closing:对自身子树加 inert,可交互范围明确为新的顶层;
 * - close/finalize 幂等。
 */
export class LayerManager {
  /** 栈拓扑世代号:open/close 时递增。 */
  generation = 0;
  readonly inert: InertRegistry = new InertRegistry();
  readonly focus: FocusManager;
  private tree: NodeTree;
  private scheduler: Scheduler;
  private stack: Layer[] = [];
  private closingLayers: Layer[] = [];
  private pendingRestore: PendingRestore | null = null;

  constructor(tree: NodeTree, scheduler: Scheduler) {
    this.tree = tree;
    this.scheduler = scheduler;
    this.focus = new FocusManager(tree, this.inert, () => this.scopeRoot());
    tree.onRemove((removed) => {
      this.inert.drop(removed);
      this.focus.ensureActiveValid();
    });
    // 异步挂载:新节点出现时重试尚未满足的焦点恢复(仍受分代保护)。
    tree.onMount(() => {
      if (this.pendingRestore !== null) {
        this.scheduler.enqueue(() => this.attemptRestore());
      }
    });
  }

  /** 当前可交互范围的根:顶层弹层,或文档根。 */
  scopeRoot(): NodeId {
    const top = this.stack[this.stack.length - 1];
    return top ? top.rootId : this.tree.rootId;
  }

  /** 显式查询可交互范围(退出动画存活期间同样明确)。 */
  interactiveScope(): { kind: "layer" | "document"; rootId: NodeId; layerId: string | null } {
    const top = this.stack[this.stack.length - 1];
    return top
      ? { kind: "layer", rootId: top.rootId, layerId: top.id }
      : { kind: "document", rootId: this.tree.rootId, layerId: null };
  }

  topLayerId(): string | null {
    const top = this.stack[this.stack.length - 1];
    return top ? top.id : null;
  }

  /** closing 中(退出动画存活)的层根。 */
  closingRoots(): NodeId[] {
    return this.closingLayers.map((layer) => layer.rootId);
  }

  isInteractive(id: NodeId): boolean {
    if (!this.tree.has(id) || !this.tree.isVisible(id)) return false;
    if (this.inert.isInert(id, this.tree)) return false;
    return this.tree.contains(this.scopeRoot(), id);
  }

  /** 指针分派:命中节点不在可交互范围内时事件被吞掉。 */
  dispatchPointer(x: number, y: number, rects: Map<NodeId, Rect>): NodeId | null {
    const hit = hitTest(rects, this.tree.documentOrder(), x, y);
    return hit !== null && this.isInteractive(hit) ? hit : null;
  }

  open(desc: LayerDescriptor): void {
    const rootId = this.tree.mount(this.tree.rootId, desc.content);
    const layer: Layer = {
      id: desc.id,
      rootId,
      triggerId: desc.triggerId,
      dismissible: desc.dismissible ?? true,
      exitAnimation: desc.exitAnimation ?? false,
      phase: "active",
      holds: [],
      selfHold: null,
    };
    // 新层之外的所有顶层区域获得一份 inert 引用(嵌套时计数叠加)。
    for (const region of this.tree.topLevelRegions()) {
      if (region !== rootId) layer.holds.push(this.inert.acquire(region));
    }
    this.stack.push(layer);
    this.generation++;
    this.focus.ensureActiveValid();
    this.scheduleRestore(desc.initialFocusId ?? null);
  }

  /** 幂等:未知/已在关闭/已收尾的 id 返回 false,不产生副作用。 */
  close(id: string): boolean {
    const idx = this.stack.findIndex((layer) => layer.id === id);
    if (idx === -1) return false;
    const wasTop = idx === this.stack.length - 1;
    const [layer] = this.stack.splice(idx, 1);
    layer.phase = "closing";
    this.closingLayers.push(layer);
    // 释放本层对其他区域的引用:内层关闭不触碰外层持有的引用。
    this.inert.releaseAll(layer.holds);
    layer.holds = [];
    // 退出动画存活期间,closing 层自身明确不可交互。
    layer.selfHold = this.inert.acquire(layer.rootId);
    this.generation++;
    this.focus.ensureActiveValid();
    if (wasTop || this.pendingRestore === null || this.pendingRestore.gen !== this.generation) {
      this.scheduleRestore(wasTop ? layer.triggerId : null);
    }
    if (!layer.exitAnimation) this.finalize(layer.id);
    return true;
  }

  /** 退出动画结束:卸载子树并释放自身 inert 引用。幂等。 */
  finalize(id: string): boolean {
    const idx = this.closingLayers.findIndex((layer) => layer.id === id);
    if (idx === -1) return false;
    const [layer] = this.closingLayers.splice(idx, 1);
    if (layer.selfHold !== null) {
      this.inert.release(layer.selfHold);
      layer.selfHold = null;
    }
    this.tree.remove(layer.rootId);
    return true;
  }

  /** Escape 只关闭可关闭的顶层;顶层不可关闭时不向下穿透。 */
  escape(): boolean {
    const top = this.stack[this.stack.length - 1];
    if (!top || !top.dismissible) return false;
    return this.close(top.id);
  }

  /** 焦点请求按当前代提交,异步执行;执行时核对代数。 */
  private scheduleRestore(preferredId: NodeId | null): void {
    const gen = this.generation;
    this.pendingRestore = { gen, preferredId };
    this.scheduler.enqueue(() => {
      if (gen !== this.generation) return; // 过时请求不得夺焦
      this.attemptRestore();
    });
  }

  private attemptRestore(): void {
    const pending = this.pendingRestore;
    if (pending === null || pending.gen !== this.generation) return;
    const target = this.resolveTarget(pending.preferredId);
    if (target !== null) {
      this.focus.focus(target);
      this.pendingRestore = null;
    }
    // 目标尚未挂载:保留请求,由挂载事件触发重试。
  }

  /** 恢复链:触发者 → 范围内 autofocus → 范围内首个可聚焦节点。 */
  private resolveTarget(preferredId: NodeId | null): NodeId | null {
    if (preferredId !== null && this.focus.isFocusable(preferredId)) return preferredId;
    const cands = this.focus.candidates();
    const auto = cands.find((id) => this.tree.get(id)?.autofocus === true);
    return auto ?? cands[0] ?? null;
  }

  /** 供状态探索做指纹与不变量检查。 */
  debugState(): unknown {
    return {
      stack: this.stack.map((layer) => layer.id),
      closing: this.closingLayers.map((layer) => layer.id),
      focus: this.focus.active,
      inert: this.inert.snapshot(),
    };
  }
}
