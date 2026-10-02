/**
 * AW-LWW-Map — Add-Wins Last-Writer-Wins Map CRDT (state-based).
 *
 * 语义：
 *  - 每个键的每次“写入/触碰”生成全局唯一标签 tag = `${nodeId}:${counter}`。
 *  - remove(key) 只墓碑化它“因果上观察到”的 tag（因果删除）。
 *  - 合并 = adds/removes 集合求并；键存活 ⟺ 存在未被墓碑覆盖的 add tag。
 *    因此并发的 remove 与 update 冲突时，update 产生的新 tag 不在 remove 的
 *    墓碑集中 → 更新优先保留（Add-Wins）。
 *  - 同一键多个存活写入之间按 LWW 仲裁：(ts, nodeId, tag) 字典序最大者胜。
 *  - 值可以是原始值或嵌套 AWLWWMap（子字段动态增删改）；存活的嵌套 Map
 *    值会递归合并，保证并发子字段更新不丢失。
 */

export type Primitive = string | number | boolean | null;
export type CRDTValue = Primitive | AWLWWMap;

interface LWWValue {
  ts: number;
  node: string;
  value: CRDTValue;
}

interface KeyEntry {
  /** tag -> 写入值 */
  adds: Map<string, LWWValue>;
  /** 被因果删除观察到的 tag 集合（墓碑） */
  removes: Set<string>;
}

/** LWW 全序：先比时间戳，再比节点 ID，最后比 tag，保证所有副本仲裁结果一致。 */
function compareLWW(tagA: string, a: LWWValue, tagB: string, b: LWWValue): number {
  if (a.ts !== b.ts) return a.ts - b.ts;
  if (a.node !== b.node) return a.node < b.node ? -1 : 1;
  return tagA < tagB ? -1 : tagA > tagB ? 1 : 0;
}

function cloneValue(value: CRDTValue): CRDTValue {
  return value instanceof AWLWWMap ? value.clone() : value;
}

export class AWLWWMap {
  private readonly nodeId: string;
  private counter = 0;
  private readonly entries = new Map<string, KeyEntry>();
  private readonly clock: () => number;

  constructor(nodeId: string, clock: () => number = () => Date.now()) {
    this.nodeId = nodeId;
    this.clock = clock;
  }

  /** 写入一个键（值可为原始值或嵌套 AWLWWMap）。 */
  set(key: string, value: CRDTValue, ts?: number): void {
    this.touch(key, value, ts);
  }

  /** 删除键：仅墓碑化当前已观察到的 add tag（因果删除）。 */
  remove(key: string): void {
    const entry = this.entries.get(key);
    if (!entry) return;
    for (const tag of entry.adds.keys()) entry.removes.add(tag);
  }

  has(key: string): boolean {
    return this.resolve(key) !== undefined;
  }

  get(key: string): CRDTValue | undefined {
    return this.resolve(key);
  }

  keys(): string[] {
    const out: string[] = [];
    for (const key of this.entries.keys()) {
      if (this.resolve(key) !== undefined) out.push(key);
    }
    return out.sort();
  }

  /** 写入嵌套子字段：user_profile.age = 25 这类操作。 */
  setField(key: string, field: string, value: CRDTValue, ts?: number): void {
    const nested = this.ensureNested(key, ts);
    nested.set(field, value, ts);
  }

  /** 删除嵌套子字段。 */
  removeField(key: string, field: string, ts?: number): void {
    const existing = this.resolve(key);
    if (!(existing instanceof AWLWWMap)) return;
    existing.remove(field);
    this.touch(key, existing, ts);
  }

  getField(key: string, field: string): CRDTValue | undefined {
    const nested = this.resolve(key);
    return nested instanceof AWLWWMap ? nested.get(field) : undefined;
  }

  /** 状态合并：对 adds/removes 求并，满足交换律/结合律/幂等。 */
  merge(other: AWLWWMap): this {
    for (const [key, otherEntry] of other.entries) {
      let entry = this.entries.get(key);
      if (!entry) {
        entry = { adds: new Map(), removes: new Set() };
        this.entries.set(key, entry);
      }
      for (const [tag, lww] of otherEntry.adds) {
        if (!entry.adds.has(tag)) {
          entry.adds.set(tag, { ts: lww.ts, node: lww.node, value: cloneValue(lww.value) });
        }
      }
      for (const tag of otherEntry.removes) entry.removes.add(tag);
    }
    return this;
  }

  /** 深拷贝（嵌套 Map 一并复制，避免副本间共享引用）。 */
  clone(): AWLWWMap {
    const copy = new AWLWWMap(this.nodeId, this.clock);
    copy.counter = this.counter;
    for (const [key, entry] of this.entries) {
      const adds = new Map<string, LWWValue>();
      for (const [tag, lww] of entry.adds) {
        adds.set(tag, { ts: lww.ts, node: lww.node, value: cloneValue(lww.value) });
      }
      copy.entries.set(key, { adds, removes: new Set(entry.removes) });
    }
    return copy;
  }

  /** 导出普通 JS 对象快照（嵌套 Map 递归展开）。 */
  toObject(): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    for (const key of this.keys()) {
      const value = this.resolve(key);
      out[key] = value instanceof AWLWWMap ? value.toObject() : value;
    }
    return out;
  }

  private touch(key: string, value: CRDTValue, ts?: number): void {
    const tag = `${this.nodeId}:${++this.counter}`;
    let entry = this.entries.get(key);
    if (!entry) {
      entry = { adds: new Map(), removes: new Set() };
      this.entries.set(key, entry);
    }
    entry.adds.set(tag, { ts: ts ?? this.clock(), node: this.nodeId, value });
  }

  private ensureNested(key: string, ts?: number): AWLWWMap {
    const existing = this.resolve(key);
    const nested = existing instanceof AWLWWMap ? existing : new AWLWWMap(this.nodeId, this.clock);
    this.touch(key, nested, ts);
    return nested;
  }

  /** 解析键的当前值：无存活 tag → undefined；否则按 LWW 仲裁并递归合并存活的嵌套 Map。 */
  private resolve(key: string): CRDTValue | undefined {
    const entry = this.entries.get(key);
    if (!entry) return undefined;

    const live: Array<[string, LWWValue]> = [];
    for (const [tag, lww] of entry.adds) {
      if (!entry.removes.has(tag)) live.push([tag, lww]);
    }
    if (live.length === 0) return undefined;

    live.sort((a, b) => compareLWW(b[0], b[1], a[0], a[1])); // 降序，最大者在前
    const top = live[0][1];

    if (top.value instanceof AWLWWMap) {
      const merged = top.value.clone();
      for (const [, lww] of live.slice(1)) {
        if (lww.value instanceof AWLWWMap) merged.merge(lww.value);
      }
      return merged;
    }
    return top.value;
  }
}
