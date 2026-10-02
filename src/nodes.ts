/**
 * CRDT 节点层：LWWRegister / MapCRDT / ArrayCRDT 三种节点可任意混合嵌套。
 *
 * 全部为状态型 CvRDT：merge(other) 满足交换律、结合律、幂等律，
 * 且均为“原地合并”——已存在的节点对象被就地更新，绝不替换引用，
 * 因此持有子树引用的协作方在合并后引用依然有效。
 */
import { compareStamps, compareTags, tagOf } from './stamp.ts';
import type { Stamp, Tag } from './stamp.ts';

export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonObject | JsonValue[];
export interface JsonObject {
  [key: string]: JsonValue;
}

export interface CRDTNode {
  /** 物化为普通 JSON 值。 */
  value(): JsonValue;
  /** 原地合并对方状态，返回 this。 */
  merge(other: CRDTNode): CRDTNode;
  /** 深拷贝（fork 副本时使用）。 */
  clone(): CRDTNode;
}

/* ------------------------------------------------------------------ */
/* LWW Register                                                        */
/* ------------------------------------------------------------------ */

export class LWWRegister implements CRDTNode {
  private val: JsonPrimitive;
  private stamp: Stamp;

  constructor(value: JsonPrimitive, stamp: Stamp) {
    this.val = value;
    this.stamp = stamp;
  }

  set(value: JsonPrimitive, stamp: Stamp): void {
    if (compareStamps(stamp, this.stamp) > 0) {
      this.val = value;
      this.stamp = stamp;
    }
  }

  value(): JsonPrimitive {
    return this.val;
  }

  merge(other: CRDTNode): this {
    if (!(other instanceof LWWRegister)) return this;
    if (compareStamps(other.stamp, this.stamp) > 0) {
      this.val = other.val;
      this.stamp = other.stamp;
    }
    return this;
  }

  clone(): LWWRegister {
    return new LWWRegister(this.val, this.stamp);
  }
}

/* ------------------------------------------------------------------ */
/* Map (OR-Map 语义：键级墓碑 + 同 tag 递归合并 + 异 tag LWW 裁决)      */
/* ------------------------------------------------------------------ */

interface MapEntry {
  readonly tag: Tag;
  node: CRDTNode;
}

export class MapCRDT implements CRDTNode {
  private readonly entries = new Map<string, MapEntry>();
  private readonly tombstones = new Set<Tag>();

  /** 写入键：若已有同键条目则墓碑化旧 tag 后替换。 */
  set(key: string, node: CRDTNode, stamp: Stamp): void {
    const existing = this.entries.get(key);
    if (existing) this.tombstones.add(existing.tag);
    this.entries.set(key, { tag: tagOf(stamp), node });
  }

  /** 删除键：墓碑化当前 tag。 */
  delete(key: string): boolean {
    const existing = this.entries.get(key);
    if (!existing) return false;
    this.tombstones.add(existing.tag);
    this.entries.delete(key);
    return true;
  }

  get(key: string): CRDTNode | undefined {
    return this.entries.get(key)?.node;
  }

  has(key: string): boolean {
    return this.entries.has(key);
  }

  keys(): IterableIterator<string> {
    return this.entries.keys();
  }

  value(): JsonObject {
    const out: JsonObject = {};
    for (const [key, entry] of this.entries) out[key] = entry.node.value();
    return out;
  }

  merge(other: CRDTNode): this {
    if (!(other instanceof MapCRDT)) return this;
    for (const tag of other.tombstones) this.tombstones.add(tag);
    for (const [key, otherEntry] of other.entries) {
      if (this.tombstones.has(otherEntry.tag)) continue;
      const local = this.entries.get(key);
      if (!local) {
        this.entries.set(key, { tag: otherEntry.tag, node: otherEntry.node.clone() });
      } else if (local.tag === otherEntry.tag) {
        // 同一逻辑键：递归原地合并，保留本地节点对象引用。
        local.node.merge(otherEntry.node);
      } else if (compareTags(otherEntry.tag, local.tag) > 0) {
        // 并发写同一键的不同值：LWW 裁决，败者墓碑化。
        this.tombstones.add(local.tag);
        this.entries.set(key, { tag: otherEntry.tag, node: otherEntry.node.clone() });
      } else {
        this.tombstones.add(otherEntry.tag);
      }
    }
    // 应用墓碑（含本地条目被对方删除的情形）。
    for (const [key, entry] of this.entries) {
      if (this.tombstones.has(entry.tag)) this.entries.delete(key);
    }
    return this;
  }

  clone(): MapCRDT {
    const copy = new MapCRDT();
    for (const tag of this.tombstones) copy.tombstones.add(tag);
    for (const [key, entry] of this.entries) {
      copy.entries.set(key, { tag: entry.tag, node: entry.node.clone() });
    }
    return copy;
  }
}

/* ------------------------------------------------------------------ */
/* Array (RGA：唯一元素 ID + 父链定序 + 墓碑删除)                        */
/* ------------------------------------------------------------------ */

interface ArrayElement {
  readonly id: Tag;
  readonly parentId: Tag | null;
  node: CRDTNode;
  deleted: boolean;
}

export class ArrayCRDT implements CRDTNode {
  private readonly elements: ArrayElement[] = [];

  /** 在 parentId 之后插入元素；parentId 为 null 表示插到头部。 */
  insertAfter(parentId: Tag | null, node: CRDTNode, stamp: Stamp): Tag {
    const id = tagOf(stamp);
    this.elements.push({ id, parentId, node, deleted: false });
    return id;
  }

  /** 追加到末尾，返回新元素 ID。 */
  push(node: CRDTNode, stamp: Stamp): Tag {
    const live = this.ordered();
    const parentId = live.length > 0 ? live[live.length - 1].id : null;
    return this.insertAfter(parentId, node, stamp);
  }

  /** 按元素 ID 墓碑化删除。 */
  removeById(id: Tag): boolean {
    const el = this.elements.find((e) => e.id === id);
    if (!el || el.deleted) return false;
    el.deleted = true;
    return true;
  }

  /** 当前可见（未删除）元素，按 RGA 定序。 */
  private ordered(): ArrayElement[] {
    const children = new Map<Tag | null, ArrayElement[]>();
    for (const el of this.elements) {
      const bucket = children.get(el.parentId);
      if (bucket) bucket.push(el);
      else children.set(el.parentId, [el]);
    }
    const out: ArrayElement[] = [];
    const visit = (parentId: Tag | null): void => {
      const kids = children.get(parentId);
      if (!kids) return;
      // 同父并发插入：时间戳新者在前，平局按 ID 字典序，保证全副本定序一致。
      kids.sort((a, b) => compareTags(b.id, a.id));
      for (const kid of kids) {
        out.push(kid);
        visit(kid.id);
      }
    };
    visit(null);
    return out.filter((el) => !el.deleted);
  }

  get length(): number {
    return this.ordered().length;
  }

  /** 按可见下标取节点。 */
  at(index: number): CRDTNode | undefined {
    return this.ordered()[index]?.node;
  }

  /** 按可见下标取元素 ID（删除/插入定位用）。 */
  idAt(index: number): Tag | undefined {
    return this.ordered()[index]?.id;
  }

  value(): JsonValue[] {
    return this.ordered().map((el) => el.node.value());
  }

  merge(other: CRDTNode): this {
    if (!(other instanceof ArrayCRDT)) return this;
    const byId = new Map<Tag, ArrayElement>(this.elements.map((el) => [el.id, el]));
    for (const otherEl of other.elements) {
      const local = byId.get(otherEl.id);
      if (!local) {
        this.elements.push({
          id: otherEl.id,
          parentId: otherEl.parentId,
          node: otherEl.node.clone(),
          deleted: otherEl.deleted,
        });
      } else {
        local.deleted = local.deleted || otherEl.deleted;
        // 同一元素：递归原地合并，保留本地节点对象引用。
        local.node.merge(otherEl.node);
      }
    }
    return this;
  }

  clone(): ArrayCRDT {
    const copy = new ArrayCRDT();
    for (const el of this.elements) {
      copy.elements.push({
        id: el.id,
        parentId: el.parentId,
        node: el.node.clone(),
        deleted: el.deleted,
      });
    }
    return copy;
  }
}

/* ------------------------------------------------------------------ */
/* 由普通 JSON 构建 CRDT 子树                                            */
/* ------------------------------------------------------------------ */

export type Tick = () => Stamp;

export function nodeFrom(value: JsonValue, tick: Tick): CRDTNode {
  if (Array.isArray(value)) {
    const arr = new ArrayCRDT();
    for (const item of value) arr.push(nodeFrom(item, tick), tick());
    return arr;
  }
  if (value !== null && typeof value === 'object') {
    const map = new MapCRDT();
    for (const [key, item] of Object.entries(value)) {
      map.set(key, nodeFrom(item, tick), tick());
    }
    return map;
  }
  return new LWWRegister(value, tick());
}
