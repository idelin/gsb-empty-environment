/**
 * 物理块存储：值被哈希构造（hash-consing）为不可变块 DAG。
 * 相同的子结构在物理上只存一份并被多方共享（引用计数），
 * 账本按物理块计费——共享块无论被引用多少次都只计一次。
 */

import { Ledger } from './ledger.ts';

export interface BlockInfo {
  id: number;
  size: number;
  refs: number;
  children: number[];
}

type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

function assertJson(value: unknown): asserts value is Json {
  if (value === null) return;
  const t = typeof value;
  if (t === 'boolean' || t === 'number' || t === 'string') return;
  if (Array.isArray(value)) {
    for (const v of value) assertJson(v);
    return;
  }
  if (t === 'object') {
    for (const k of Object.keys(value as Record<string, unknown>)) {
      assertJson((value as Record<string, unknown>)[k]);
    }
    return;
  }
  throw new Error(`value is not JSON-serializable (type=${t})`);
}

/** 逻辑序列化尺寸（规范化 JSON，键排序），用于测试与文档中的尺寸推算。 */
export function byteSize(value: unknown): number {
  return canonicalJson(value).length;
}

function canonicalJson(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) as string;
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(',')}]`;
  const obj = value as Record<string, unknown>;
  const parts = Object.keys(obj)
    .sort()
    .map((k) => `${JSON.stringify(k)}:${canonicalJson(obj[k])}`);
  return `{${parts.join(',')}}`;
}

export class BlockStore {
  private ledger: Ledger;
  private blocks = new Map<number, { size: number; refs: number; children: number[] }>();
  private byKey = new Map<string, number>();
  private payloads = new Map<number, string>();
  private nextId = 1;

  constructor(ledger: Ledger) {
    this.ledger = ledger;
  }

  /**
   * 将值物化为块 DAG，返回根块 id（调用者持有 1 个引用）。
   * 结构上相同的子值复用已有物理块。
   */
  intern(value: unknown): number {
    assertJson(value);
    return this.internNode(value as Json);
  }

  private internNode(value: Json): number {
    let key: string;
    let children: number[] = [];
    if (value === null || typeof value !== 'object') {
      key = `v:${JSON.stringify(value)}`;
    } else if (Array.isArray(value)) {
      children = value.map((v) => this.internNode(v));
      key = `a:[${children.join(',')}]`;
    } else {
      const keys = Object.keys(value).sort();
      const parts: string[] = [];
      for (const k of keys) {
        const child = this.internNode((value as Record<string, Json>)[k]);
        children.push(child);
        parts.push(`${JSON.stringify(k)}:${child}`);
      }
      key = `o:{${parts.join(',')}}`;
    }
    const existing = this.byKey.get(key);
    if (existing !== undefined) {
      // 复用已有物理块：回收为构造 key 而新建的子引用，改为对共享块 +1。
      for (const c of children) this.release(c);
      this.retain(existing);
      return existing;
    }
    this.ledger.charge(key.length);
    const id = this.nextId++;
    this.blocks.set(id, { size: key.length, refs: 1, children });
    this.payloads.set(id, key);
    this.byKey.set(key, id);
    return id;
  }

  /** 物化出普通 JS 值（深拷贝，与块存储脱钩）。 */
  resolve(id: number): unknown {
    const key = this.payloads.get(id);
    if (key === undefined) throw new Error(`resolve: unknown block ${id}`);
    if (key.startsWith('v:')) return JSON.parse(key.slice(2));
    if (key.startsWith('a:')) {
      const inner = key.slice(3, -1);
      const children = inner === '' ? [] : inner.split(',').map(Number);
      return children.map((c) => this.resolve(c));
    }
    const inner = key.slice(3, -1);
    const out: Record<string, unknown> = {};
    if (inner !== '') {
      for (const part of splitTopLevel(inner)) {
        const sep = keyBoundary(part);
        const k = JSON.parse(part.slice(0, sep)) as string;
        out[k] = this.resolve(Number(part.slice(sep + 1)));
      }
    }
    return out;
  }

  retain(id: number): void {
    const b = this.blocks.get(id);
    if (!b) throw new Error(`retain: unknown block ${id}`);
    b.refs++;
  }

  release(id: number): void {
    const b = this.blocks.get(id);
    if (!b) throw new Error(`release: unknown block ${id}`);
    b.refs--;
    if (b.refs < 0) throw new Error(`block ${id} refcount underflow`);
    if (b.refs === 0) {
      this.blocks.delete(id);
      this.byKey.delete(this.payloads.get(id) as string);
      this.payloads.delete(id);
      this.ledger.discharge(b.size);
      for (const c of b.children) this.release(c);
    }
  }

  has(id: number): boolean {
    return this.blocks.has(id);
  }

  sizeOf(id: number): number {
    const b = this.blocks.get(id);
    if (!b) throw new Error(`sizeOf: unknown block ${id}`);
    return b.size;
  }

  refsOf(id: number): number {
    const b = this.blocks.get(id);
    if (!b) throw new Error(`refsOf: unknown block ${id}`);
    return b.refs;
  }

  childrenOf(id: number): number[] {
    const b = this.blocks.get(id);
    if (!b) throw new Error(`childrenOf: unknown block ${id}`);
    return [...b.children];
  }

  /** 全部存活块的快照（供独立遍历核验使用）。 */
  listBlocks(): BlockInfo[] {
    const out: BlockInfo[] = [];
    for (const [id, b] of this.blocks) {
      out.push({ id, size: b.size, refs: b.refs, children: [...b.children] });
    }
    return out;
  }
}

/** 找到 `"key":child` 中键与冒号的分界（冒号下标），正确处理键内转义引号。 */
function keyBoundary(part: string): number {
  let i = 1; // 跳过起始引号
  while (i < part.length) {
    if (part[i] === '\\') i += 2;
    else if (part[i] === '"') return i + 1; // 冒号位置
    else i++;
  }
  throw new Error(`malformed object payload part: ${part}`);
}

function splitTopLevel(s: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let start = 0;
  let inString = false;
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (inString) {
      if (ch === '\\') i++;
      else if (ch === '"') inString = false;
      continue;
    }
    if (ch === '"') inString = true;
    else if (ch === '{' || ch === '[') depth++;
    else if (ch === '}' || ch === ']') depth--;
    else if (ch === ',' && depth === 0) {
      parts.push(s.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(s.slice(start));
  return parts;
}
