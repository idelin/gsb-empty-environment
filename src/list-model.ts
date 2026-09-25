import { FenwickIndex, type PrefixIndex } from "./height-index.ts";

export interface ItemSpec {
  id: string;
  /** 未实测前使用的估计行高；缺省用模型的 defaultEstimate。 */
  estimate?: number;
  /** 所属分组 id；分组折叠时该条目有效高度为 0。 */
  parentId?: string;
}

/** 异步测量令牌：签发时捕获条目当前的测量纪元。 */
export interface MeasureToken {
  readonly id: string;
  readonly epoch: number;
}

interface ItemRecord {
  /** 最近一次已知（实测或估计）高度。 */
  height: number;
  parentId: string | null;
  /** 测量纪元：插入为 0，每次重排（move）后 +1，使重排前签发的测量令牌失效。 */
  epoch: number;
}

/**
 * 可变行高列表模型：扁平条目序列 + 分组折叠 + 估计/实测高度。
 *
 * 公开后备规则（测量相关）：
 * - 令牌对应的条目已删除 → 测量被忽略，applyMeasure 返回 false；
 * - 令牌签发后条目被重排（epoch 变化）→ 迟到测量被忽略，返回 false；
 * - 分组折叠期间到达的有效测量仍记录高度，但有效高度保持 0，展开后生效。
 */
export class ListModel {
  readonly index: PrefixIndex;
  readonly defaultEstimate: number;
  private order: string[] = [];
  private items = new Map<string, ItemRecord>();
  private collapsed = new Set<string>();

  constructor(defaultEstimate = 40, index?: PrefixIndex) {
    this.defaultEstimate = defaultEstimate;
    this.index = index ?? new FenwickIndex();
  }

  get size(): number {
    return this.order.length;
  }

  /** 高度索引版本号，任何影响布局的修改都会使其递增。 */
  get version(): number {
    return this.index.version;
  }

  has(id: string): boolean {
    return this.items.has(id);
  }

  idAt(index: number): string {
    return this.order[index];
  }

  ids(): string[] {
    return [...this.order];
  }

  indexOf(id: string): number {
    return this.order.indexOf(id);
  }

  /** 有效高度：所属分组折叠时为 0。 */
  heightOf(id: string): number {
    const rec = this.require(id);
    return rec.parentId !== null && this.collapsed.has(rec.parentId) ? 0 : rec.height;
  }

  /** 最近一次已知高度（忽略折叠状态）。 */
  storedHeightOf(id: string): number {
    return this.require(id).height;
  }

  offsetOf(id: string): number {
    const i = this.indexOf(id);
    if (i < 0) throw new RangeError(`unknown item id: ${id}`);
    return this.index.prefix(i);
  }

  totalHeight(): number {
    return this.index.total();
  }

  isCollapsed(id: string): boolean {
    return this.collapsed.has(id);
  }

  insert(index: number, spec: ItemSpec): void {
    if (this.items.has(spec.id)) {
      throw new Error(`duplicate item id: ${spec.id}`);
    }
    if (index < 0 || index > this.order.length) {
      throw new RangeError(`insert index ${index} out of range`);
    }
    const height = spec.estimate ?? this.defaultEstimate;
    this.items.set(spec.id, {
      height,
      parentId: spec.parentId ?? null,
      epoch: 0,
    });
    this.order.splice(index, 0, spec.id);
    this.index.insert(index, this.effectiveHeight(spec.id));
  }

  append(spec: ItemSpec): void {
    this.insert(this.order.length, spec);
  }

  remove(id: string): boolean {
    const i = this.indexOf(id);
    if (i < 0) return false;
    this.order.splice(i, 1);
    this.items.delete(id);
    this.collapsed.delete(id);
    this.index.remove(i);
    return true;
  }

  /** 重排：把条目移动到新的显示位置；使该条目此前签发的测量令牌失效。 */
  move(id: string, toIndex: number): void {
    const from = this.indexOf(id);
    if (from < 0) throw new RangeError(`unknown item id: ${id}`);
    if (toIndex < 0 || toIndex >= this.order.length) {
      throw new RangeError(`move index ${toIndex} out of range`);
    }
    if (from === toIndex) return;
    const rec = this.require(id);
    rec.epoch++;
    this.order.splice(from, 1);
    this.order.splice(toIndex, 0, id);
    this.index.remove(from);
    this.index.insert(toIndex, this.effectiveHeight(id));
  }

  /** 折叠/展开分组：组内条目有效高度变为 0 / 恢复为已知高度。 */
  setCollapsed(id: string, collapsed: boolean): void {
    if (collapsed === this.collapsed.has(id)) return;
    if (collapsed) {
      this.collapsed.add(id);
    } else {
      this.collapsed.delete(id);
    }
    for (let i = 0; i < this.order.length; i++) {
      const rec = this.items.get(this.order[i])!;
      if (rec.parentId === id) {
        this.index.set(i, collapsed ? 0 : rec.height);
      }
    }
  }

  /** 为异步测量签发令牌；条目不存在时返回 null。 */
  requestMeasure(id: string): MeasureToken | null {
    const rec = this.items.get(id);
    if (!rec) return null;
    return { id, epoch: rec.epoch };
  }

  /**
   * 应用异步测量结果。遵守公开后备规则：
   * 条目已删除或令牌已过期（重排后）时忽略并返回 false。
   */
  applyMeasure(token: MeasureToken, height: number): boolean {
    const rec = this.items.get(token.id);
    if (!rec) return false;
    if (token.epoch !== rec.epoch) return false;
    if (!Number.isFinite(height) || height < 0) {
      throw new RangeError(`measured height must be a finite number >= 0, got ${height}`);
    }
    if (rec.height === height) return true;
    rec.height = height;
    const collapsed =
      rec.parentId !== null && this.collapsed.has(rec.parentId);
    if (!collapsed) {
      this.index.set(this.indexOf(token.id), height);
    }
    return true;
  }

  private effectiveHeight(id: string): number {
    const rec = this.require(id);
    return rec.parentId !== null && this.collapsed.has(rec.parentId) ? 0 : rec.height;
  }

  private require(id: string): ItemRecord {
    const rec = this.items.get(id);
    if (!rec) throw new RangeError(`unknown item id: ${id}`);
    return rec;
  }
}
