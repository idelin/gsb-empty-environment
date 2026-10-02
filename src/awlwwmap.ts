/**
 * AW-LWW-Map: Add-Wins Last-Writer-Wins Map (CRDT)
 *
 * 语义：
 *  - Add-Wins（添加胜出）：并发的「删除键」与「更新键内字段」产生因果竞态时，
 *    未被删除操作观测到的更新会存活，键被保留（更新优先）。
 *  - LWW（最后写入胜出）：同一字段的并发赋值按 (timestamp, nodeId) 字典序仲裁。
 *
 * 实现模型（参考 delta-CRDT AWMap）：
 *  - 每次字段写入产生唯一 Dot（`${nodeId}:${counter}`），记录到键的存活点集中。
 *  - 每个字段版本携带产生它的 Dot；字段存活当且仅当其 Dot 在合并后的存活点集中。
 *  - 删除键 = 清空该键当前观测到的 Dot（点仍保留在因果上下文 causal context 中，
 *    用于合并时识别「已知但被删除」的点，避免旧状态复活）。
 *  - 合并时点集 join 规则：(A∩B) ∪ (A\ctxB) ∪ (B\ctxA)。
 */

export type Dot = string;
export type Value = string | number | boolean | null;

/** 字段版本：LWW 寄存器（deleted=true 表示字段级墓碑） */
export interface FieldVersion {
  dot: Dot;
  ts: number;
  node: string;
  value: Value;
  deleted: boolean;
}

/** 键条目：存活点集 + 字段版本表 */
export interface KeyEntry {
  dots: Set<Dot>;
  fields: Map<string, FieldVersion>;
}

export interface KeyEntrySnapshot {
  dots: Dot[];
  fields: Record<string, FieldVersion>;
}

export interface MapSnapshot {
  nodeId: string;
  counter: number;
  context: Dot[];
  keys: Record<string, KeyEntrySnapshot>;
}

/** 比较两个 (ts, node) 版本戳，a 更新则返回 true */
function newer(tsA: number, nodeA: string, tsB: number, nodeB: string): boolean {
  return tsA > tsB || (tsA === tsB && nodeA > nodeB);
}

export class AWLWWMap {
  readonly nodeId: string;
  private counter = 0;
  /** 因果上下文：本副本见过的所有 Dot（含已删除的） */
  private context: Set<Dot> = new Set();
  /** 键存储：仅保存当前存活的键（删除键即移除条目，点留在 context） */
  private keys: Map<string, KeyEntry> = new Map();

  constructor(nodeId: string) {
    if (!nodeId) throw new Error("nodeId must be non-empty");
    this.nodeId = nodeId;
  }

  private nextDot(): Dot {
    this.counter += 1;
    return `${this.nodeId}:${this.counter}`;
  }

  /** 写入/更新字段；键不存在则隐式创建（动态增改子字段） */
  set(key: string, field: string, value: Value, ts: number = Date.now()): void {
    const dot = this.nextDot();
    this.context.add(dot);
    let entry = this.keys.get(key);
    if (!entry) {
      entry = { dots: new Set(), fields: new Map() };
      this.keys.set(key, entry);
    }
    entry.dots.add(dot);
    const prev = entry.fields.get(field);
    if (!prev || newer(ts, this.nodeId, prev.ts, prev.node)) {
      entry.fields.set(field, { dot, ts, node: this.nodeId, value, deleted: false });
    } else {
      // 本地更旧的写入仍保留新 Dot 以维持键的存活语义，但字段值不变
      entry.fields.set(field, { ...prev });
    }
  }

  /** 删除字段（字段级 LWW 墓碑，动态删除子字段） */
  removeField(key: string, field: string, ts: number = Date.now()): void {
    const entry = this.keys.get(key);
    if (!entry) return;
    const dot = this.nextDot();
    this.context.add(dot);
    entry.dots.add(dot);
    const prev = entry.fields.get(field);
    if (!prev || newer(ts, this.nodeId, prev.ts, prev.node)) {
      entry.fields.set(field, { dot, ts, node: this.nodeId, value: null, deleted: true });
    }
  }

  /** 删除根键：仅移除当前观测到的 Dot；并发未观测到的更新在合并时存活（Add-Wins） */
  removeKey(key: string): void {
    // 条目从存储中移除；其 Dot 已存在于 context，合并时据此判定为已删除
    this.keys.delete(key);
  }

  has(key: string): boolean {
    return this.keys.has(key);
  }

  /** 读取键的全部存活字段；键不存在返回 undefined */
  get(key: string): Record<string, Value> | undefined {
    const entry = this.keys.get(key);
    if (!entry) return undefined;
    const out: Record<string, Value> = {};
    for (const [field, ver] of entry.fields) {
      if (!ver.deleted && entry.dots.has(ver.dot)) out[field] = ver.value;
    }
    return out;
  }

  getField(key: string, field: string): Value | undefined {
    const entry = this.keys.get(key);
    if (!entry) return undefined;
    const ver = entry.fields.get(field);
    if (!ver || ver.deleted || !entry.dots.has(ver.dot)) return undefined;
    return ver.value;
  }

  keysLive(): string[] {
    return [...this.keys.keys()];
  }

  /**
   * 合并另一副本的状态（就地合并，幂等、交换、结合）。
   * 点集 join：(A∩B) ∪ (A\ctxB) ∪ (B\ctxA)
   *  — 双方都有的点：存活；
   *  — 仅一方有且对方从未见过：存活（并发新增，Add-Wins 核心）；
   *  — 仅一方有且对方已知：已被对方删除，丢弃。
   */
  merge(other: AWLWWMap): this {
    const allKeys = new Set<string>([...this.keys.keys(), ...other.keys.keys()]);
    for (const key of allKeys) {
      const a = this.keys.get(key);
      const b = other.keys.get(key);
      const dotsA = a?.dots ?? new Set<Dot>();
      const dotsB = b?.dots ?? new Set<Dot>();

      const live = new Set<Dot>();
      for (const d of dotsA) if (dotsB.has(d)) live.add(d);
      for (const d of dotsA) if (!other.context.has(d)) live.add(d);
      for (const d of dotsB) if (!this.context.has(d)) live.add(d);

      if (live.size === 0) {
        this.keys.delete(key);
        continue;
      }

      // 字段合并：仅考虑 Dot 存活的版本，按 LWW 取胜者
      const mergedFields = new Map<string, FieldVersion>();
      const fieldNames = new Set<string>([
        ...(a ? a.fields.keys() : []),
        ...(b ? b.fields.keys() : []),
      ]);
      for (const field of fieldNames) {
        const candidates: FieldVersion[] = [];
        const va = a?.fields.get(field);
        const vb = b?.fields.get(field);
        if (va && live.has(va.dot)) candidates.push(va);
        if (vb && live.has(vb.dot)) candidates.push(vb);
        if (candidates.length === 0) continue;
        let best = candidates[0];
        for (const c of candidates.slice(1)) {
          if (newer(c.ts, c.node, best.ts, best.node)) best = c;
        }
        mergedFields.set(field, { ...best });
      }
      this.keys.set(key, { dots: live, fields: mergedFields });
    }
    for (const d of other.context) this.context.add(d);
    this.counter = Math.max(this.counter, this.maxCounterOf(other.context));
    return this;
  }

  private maxCounterOf(context: Set<Dot>): number {
    let max = this.counter;
    for (const d of context) {
      const idx = d.lastIndexOf(":");
      const n = Number(d.slice(idx + 1));
      if (d.slice(0, idx) === this.nodeId && n > max) max = n;
    }
    return max;
  }

  /** 序列化快照（用于网络传输/持久化） */
  snapshot(): MapSnapshot {
    const keys: Record<string, KeyEntrySnapshot> = {};
    for (const [key, entry] of this.keys) {
      const fields: Record<string, FieldVersion> = {};
      for (const [f, v] of entry.fields) fields[f] = { ...v };
      keys[key] = { dots: [...entry.dots], fields };
    }
    return {
      nodeId: this.nodeId,
      counter: this.counter,
      context: [...this.context],
      keys,
    };
  }

  /** 从快照恢复副本（nodeId 以快照为准，可再 merge 进任意节点） */
  static fromSnapshot(snap: MapSnapshot): AWLWWMap {
    const map = new AWLWWMap(snap.nodeId);
    map.counter = snap.counter;
    map.context = new Set(snap.context);
    for (const [key, ks] of Object.entries(snap.keys)) {
      map.keys.set(key, {
        dots: new Set(ks.dots),
        fields: new Map(Object.entries(ks.fields).map(([f, v]) => [f, { ...v }])),
      });
    }
    return map;
  }

  /** 深拷贝一个等价副本（模拟网络对端） */
  clone(nodeId?: string): AWLWWMap {
    const snap = this.snapshot();
    if (nodeId) snap.nodeId = nodeId;
    return AWLWWMap.fromSnapshot(snap);
  }
}
