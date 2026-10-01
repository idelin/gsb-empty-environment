/**
 * 焦点节点树。
 * 每个节点携带：矩形（绝对布局坐标，滚动前）、可见/遮挡信息、逻辑次序、异步挂载状态。
 * 任何几何或结构变更都会使 layoutVersion 递增，供异步完成时校验。
 */

import type { Rect } from './geometry.ts';
import { containsPoint } from './geometry.ts';

export type Orientation = 'horizontal' | 'vertical';

export interface ScrollSpec {
  /** 视口矩形（绝对坐标）。 */
  viewport: Rect;
  /** 内容总尺寸。 */
  contentSize: { w: number; h: number };
  /** 滚动动画耗时（调度器毫秒），默认 20。 */
  scrollDelay?: number;
}

export interface NodeInit {
  id: string;
  label?: string;
  rect: Rect;
  focusable?: boolean;
  visible?: boolean;
  mounted?: boolean;
  /** 模态区域根：进入后方向键不能越出其子树。 */
  modal?: boolean;
  /** 不透明遮挡层（如模态遮罩），按 z 遮挡中心点被覆盖的节点。 */
  opaque?: boolean;
  z?: number;
  collapsed?: boolean;
  orientation?: Orientation;
  scroll?: ScrollSpec;
  /** 异步挂载耗时；未设置且 mounted=false 时视为同步可挂载（delay 0）。 */
  mountDelay?: number;
  children?: NodeInit[];
}

export class FocusNode {
  id: string;
  label: string;
  rect: Rect;
  parent: FocusNode | null = null;
  children: FocusNode[] = [];
  /** 逻辑次序：深度优先文档序，用于平局裁决与合法后备。 */
  order = 0;
  focusable: boolean;
  visible: boolean;
  mounted: boolean;
  modal: boolean;
  opaque: boolean;
  z: number;
  collapsed: boolean;
  orientation: Orientation | null;
  scroll: { viewport: Rect; offset: { x: number; y: number }; contentSize: { w: number; h: number }; scrollDelay: number } | null;
  mountDelay: number;

  constructor(init: NodeInit) {
    this.id = init.id;
    this.label = init.label ?? init.id;
    this.rect = { ...init.rect };
    this.focusable = init.focusable ?? false;
    this.visible = init.visible ?? true;
    this.mounted = init.mounted ?? true;
    this.modal = init.modal ?? false;
    this.opaque = init.opaque ?? false;
    this.z = init.z ?? 0;
    this.collapsed = init.collapsed ?? false;
    this.orientation = init.orientation ?? null;
    this.scroll = init.scroll
      ? {
          viewport: { ...init.scroll.viewport },
          offset: { x: 0, y: 0 },
          contentSize: { ...init.scroll.contentSize },
          scrollDelay: init.scroll.scrollDelay ?? 20,
        }
      : null;
    this.mountDelay = init.mountDelay ?? 0;
  }
}

export class FocusTree {
  root: FocusNode;
  byId = new Map<string, FocusNode>();
  /** 布局版本：几何/可见性/挂载/结构每次变更 +1。 */
  layoutVersion = 0;
  /** 结构变更监听器（由导航器注册以执行焦点救援）。 */
  onStructureChange: ((removedOrder: number | null) => void) | null = null;

  constructor(init: NodeInit) {
    this.root = this.build(init, null);
    let order = 0;
    for (const node of this.walk()) {
      node.order = order++;
    }
  }

  private build(init: NodeInit, parent: FocusNode | null): FocusNode {
    const node = new FocusNode(init);
    node.parent = parent;
    this.byId.set(node.id, node);
    for (const childInit of init.children ?? []) {
      node.children.push(this.build(childInit, node));
    }
    return node;
  }

  *walk(scope?: FocusNode): Generator<FocusNode> {
    const start = scope ?? this.root;
    yield start;
    for (const child of start.children) {
      yield* this.walk(child);
    }
  }

  get size(): number {
    return this.byId.size;
  }

  has(id: string): boolean {
    return this.byId.has(id);
  }

  get(id: string): FocusNode {
    const node = this.byId.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    return node;
  }

  private bump(): void {
    this.layoutVersion++;
  }

  /** 删除子树；返回被删节点的逻辑次序（供后备定位）。 */
  remove(id: string): number {
    const node = this.get(id);
    if (!node.parent) throw new Error('cannot remove root');
    const removedOrder = node.order;
    node.parent.children = node.parent.children.filter((c) => c !== node);
    for (const n of this.walk(node)) {
      this.byId.delete(n.id);
    }
    this.bump();
    this.onStructureChange?.(removedOrder);
    return removedOrder;
  }

  setCollapsed(id: string, collapsed: boolean): void {
    this.get(id).collapsed = collapsed;
    this.bump();
    this.onStructureChange?.(null);
  }

  setVisible(id: string, visible: boolean): void {
    this.get(id).visible = visible;
    this.bump();
    this.onStructureChange?.(null);
  }

  setMounted(id: string, mounted: boolean): void {
    this.get(id).mounted = mounted;
    this.bump();
    this.onStructureChange?.(null);
  }

  /** 应用滚动偏移（异步滚动完成时调用），自动夹取到合法范围。 */
  applyScroll(id: string, offset: { x: number; y: number }): void {
    const node = this.get(id);
    if (!node.scroll) throw new Error(`node ${id} is not a scroll container`);
    const maxX = Math.max(0, node.scroll.contentSize.w - node.scroll.viewport.w);
    const maxY = Math.max(0, node.scroll.contentSize.h - node.scroll.viewport.h);
    node.scroll.offset = {
      x: Math.min(Math.max(0, offset.x), maxX),
      y: Math.min(Math.max(0, offset.y), maxY),
    };
    this.bump();
    this.onStructureChange?.(null);
  }

  /** 节点的有效矩形：绝对坐标减去各祖先滚动容器的偏移。 */
  effectiveRect(node: FocusNode): Rect {
    let dx = 0;
    let dy = 0;
    for (let p = node.parent; p; p = p.parent) {
      if (p.scroll) {
        dx += p.scroll.offset.x;
        dy += p.scroll.offset.y;
      }
    }
    return { x: node.rect.x - dx, y: node.rect.y - dy, w: node.rect.w, h: node.rect.h };
  }

  /** 节点是否被更高 z 的不透明层遮挡（以中心点判定）。 */
  isOccluded(node: FocusNode): boolean {
    const rect = this.effectiveRect(node);
    const cx = rect.x + rect.w / 2;
    const cy = rect.y + rect.h / 2;
    const nodeZ = this.effectiveZ(node);
    for (const other of this.walk()) {
      if (!other.opaque || this.effectiveZ(other) <= nodeZ) continue;
      if (!this.isChainVisible(other)) continue;
      if (containsPoint(this.effectiveRect(other), cx, cy)) return true;
    }
    return false;
  }

  /** 有效 z：沿祖先链取最大（简化堆叠上下文）。 */
  effectiveZ(node: FocusNode): number {
    let z = node.z;
    for (let p = node.parent; p; p = p.parent) {
      if (p.z > z) z = p.z;
    }
    return z;
  }

  /** 自身与祖先链均 visible 且未折叠。 */
  isChainVisible(node: FocusNode): boolean {
    for (let p: FocusNode | null = node; p; p = p.parent) {
      if (!p.visible || p.collapsed) return false;
    }
    return true;
  }

  /**
   * 有效可见：链路可见、未被遮挡。
   * allowUnmounted 用于“先确定候选”阶段——未挂载节点仍在布局中，可作为候选。
   */
  isEffectivelyVisible(node: FocusNode, allowUnmounted = false): boolean {
    if (!this.isChainVisible(node)) return false;
    if (!allowUnmounted && !node.mounted) return false;
    return !this.isOccluded(node);
  }

  /** 最近的滚动容器祖先（含自身）。 */
  nearestScroller(node: FocusNode): FocusNode | null {
    for (let p: FocusNode | null = node; p; p = p.parent) {
      if (p.scroll) return p;
    }
    return null;
  }

  /** 最近的可见模态祖先（含自身）；无则返回 null（表示全局作用域）。 */
  modalScopeOf(node: FocusNode): FocusNode | null {
    for (let p: FocusNode | null = node; p; p = p.parent) {
      if (p.modal && this.isChainVisible(p)) return p;
    }
    return null;
  }
}
