/**
 * NodeTree：由夹具给出的节点树 + 盒/字形度量。
 *
 * 核心计算（可见性、可聚焦性、inert 传播）全部基于夹具度量完成，
 * 不依赖任何浏览器布局引擎：
 *  - 容器节点：缺省度量视为已布局盒（无限大），显式 0 尺寸或 display:none 视为塌陷；
 *  - 文本叶子：尺寸由字形度量（逐字 advance 求和 × lineHeight）推导；
 *  - inert：按子树根引用计数，沿祖先链传播。
 */

export interface GlyphMetric {
  /** 单个字形的水平步进（px） */
  advance: number;
}

export interface NodeMetrics {
  width?: number;
  height?: number;
  display?: 'none' | 'visible';
  /** 文本叶子的行高 */
  lineHeight?: number;
  /** 文本叶子逐字字形度量 */
  glyphs?: GlyphMetric[];
}

export interface NodeSpec {
  id: string;
  focusable?: boolean;
  text?: string;
  children?: NodeSpec[];
}

interface TreeNode {
  spec: NodeSpec;
  parentId: string | null;
  childIds: string[];
}

interface Box {
  width: number;
  height: number;
  display: 'none' | 'visible';
}

export class NodeTree {
  private nodes: Map<string, TreeNode> = new Map();
  private metrics: Map<string, NodeMetrics> = new Map();
  private claims: Map<string, number> = new Map();
  private listeners: Set<() => void> = new Set();
  private root: string | null = null;

  constructor(root: NodeSpec, metrics: Record<string, NodeMetrics> = {}) {
    this.attach(root, null, metrics);
    this.root = root.id;
  }

  onMutation(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private emitMutation(): void {
    for (const listener of this.listeners) listener();
  }

  private attach(spec: NodeSpec, parentId: string | null, metrics: Record<string, NodeMetrics>): void {
    if (this.nodes.has(spec.id)) throw new Error(`duplicate node id: ${spec.id}`);
    this.nodes.set(spec.id, { spec, parentId, childIds: [] });
    if (metrics[spec.id]) this.metrics.set(spec.id, metrics[spec.id]);
    if (parentId !== null) {
      const parent = this.nodes.get(parentId);
      if (!parent) throw new Error(`unknown parent: ${parentId}`);
      parent.childIds.push(spec.id);
    }
    for (const child of spec.children ?? []) this.attach(child, spec.id, metrics);
  }

  mount(parentId: string, spec: NodeSpec, metrics: Record<string, NodeMetrics> = {}): void {
    if (!this.nodes.has(parentId)) throw new Error(`unknown parent: ${parentId}`);
    this.attach(spec, parentId, metrics);
    this.emitMutation();
  }

  unmount(id: string): boolean {
    const node = this.nodes.get(id);
    if (!node) return false;
    if (id === this.root) throw new Error('cannot unmount root');
    const parent = node.parentId !== null ? this.nodes.get(node.parentId) : undefined;
    if (parent) parent.childIds = parent.childIds.filter((child) => child !== id);
    this.detachSubtree(id);
    this.emitMutation();
    return true;
  }

  private detachSubtree(id: string): void {
    const node = this.nodes.get(id);
    if (!node) return;
    for (const child of [...node.childIds]) this.detachSubtree(child);
    this.nodes.delete(id);
    this.metrics.delete(id);
    this.claims.delete(id); // 随子树释放其上的 inert 引用
  }

  has(id: string): boolean {
    return this.nodes.has(id);
  }

  rootId(): string | null {
    return this.root;
  }

  parentOf(id: string): string | null {
    return this.nodes.get(id)?.parentId ?? null;
  }

  childIdsOf(id: string): string[] {
    return [...(this.nodes.get(id)?.childIds ?? [])];
  }

  /** rootId 是否为 id 的祖先（含自身） */
  contains(rootId: string, id: string): boolean {
    let cur: string | null = id;
    while (cur !== null) {
      if (cur === rootId) return true;
      cur = this.parentOf(cur);
    }
    return false;
  }

  private boxOf(id: string): Box {
    const node = this.nodes.get(id);
    if (!node) return { width: 0, height: 0, display: 'none' };
    const m = this.metrics.get(id);
    // 显式盒度量优先（如带文本的按钮）
    if (m?.width !== undefined || m?.height !== undefined) {
      return { width: m.width ?? 0, height: m.height ?? 0, display: m.display ?? 'visible' };
    }
    if (node.spec.text !== undefined) {
      // 无显式盒的文本叶子：由字形度量推导固有尺寸
      const glyphs = m?.glyphs ?? [];
      const width = glyphs.reduce((sum, g) => sum + g.advance, 0);
      return { width, height: m?.lineHeight ?? 0, display: m?.display ?? 'visible' };
    }
    // 容器：缺省视为已布局盒；显式 0 尺寸表示塌陷
    return {
      width: m?.width ?? Number.POSITIVE_INFINITY,
      height: m?.height ?? Number.POSITIVE_INFINITY,
      display: m?.display ?? 'visible',
    };
  }

  intrinsicSize(id: string): { width: number; height: number } {
    const box = this.boxOf(id);
    return { width: box.width, height: box.height };
  }

  /** 自身及祖先链上无 display:none 且盒非空 */
  isVisible(id: string): boolean {
    let cur: string | null = id;
    while (cur !== null) {
      const box = this.boxOf(cur);
      if (box.display === 'none' || box.width <= 0 || box.height <= 0) return false;
      cur = this.parentOf(cur);
    }
    return true;
  }

  // ---- inert 引用计数 ----

  addInertClaim(id: string): void {
    if (!this.nodes.has(id)) return;
    this.claims.set(id, (this.claims.get(id) ?? 0) + 1);
  }

  removeInertClaim(id: string): void {
    const count = this.claims.get(id);
    if (count === undefined) return; // 幂等：无引用可释放
    if (count <= 1) this.claims.delete(id);
    else this.claims.set(id, count - 1);
  }

  inertClaimCount(id: string): number {
    return this.claims.get(id) ?? 0;
  }

  totalInertClaims(): number {
    let total = 0;
    for (const count of this.claims.values()) total += count;
    return total;
  }

  /** 祖先链（含自身）上任一节点持有 inert 引用即为 inert */
  isInert(id: string): boolean {
    let cur: string | null = id;
    while (cur !== null) {
      if ((this.claims.get(cur) ?? 0) > 0) return true;
      cur = this.parentOf(cur);
    }
    return false;
  }

  isFocusable(id: string): boolean {
    const node = this.nodes.get(id);
    if (!node || !node.spec.focusable) return false;
    return this.isVisible(id) && !this.isInert(id);
  }

  /** 先序深度优先找到子树内第一个可聚焦节点 */
  firstFocusableWithin(rootId: string): string | null {
    if (!this.nodes.has(rootId)) return null;
    const visit = (id: string): string | null => {
      if (this.isFocusable(id)) return id;
      for (const child of this.nodes.get(id)?.childIds ?? []) {
        const hit = visit(child);
        if (hit !== null) return hit;
      }
      return null;
    };
    return visit(rootId);
  }

  collectFocusableWithin(rootId: string): string[] {
    const out: string[] = [];
    const visit = (id: string): void => {
      const node = this.nodes.get(id);
      if (!node) return;
      if (this.isFocusable(id)) out.push(id);
      for (const child of node.childIds) visit(child);
    };
    visit(rootId);
    return out;
  }
}
