import { defaultHash, type HashFn } from './hash.ts';
import { isObject, type JsonValue } from './types.ts';

export type NodeKind = 'null' | 'boolean' | 'number' | 'string' | 'array' | 'object';

/**
 * 内容摘要 DAG 节点。
 * 对象键无序：entries 按键排序后参与摘要；数组有序：items 按下标参与摘要。
 * 节点保留完整结构，使摘要相等时可以做碰撞复核（contentEqual）。
 */
export interface DigestNode {
  digest: string;
  kind: NodeKind;
  /** 叶子字面量值。 */
  value?: null | boolean | number | string;
  /** 对象子节点，按键排序。 */
  entries?: Array<[string, DigestNode]>;
  /** 数组子节点，按下标。 */
  items?: DigestNode[];
}

export function kindOf(v: JsonValue): NodeKind {
  if (v === null) return 'null';
  if (Array.isArray(v)) return 'array';
  if (typeof v === 'object') return 'object';
  return typeof v as 'boolean' | 'number' | 'string';
}

export function buildDigest(value: JsonValue, hash: HashFn = defaultHash): DigestNode {
  const kind = kindOf(value);
  if (kind === 'array') {
    const items = (value as JsonValue[]).map((v) => buildDigest(v, hash));
    const digest = hash(`arr[${items.map((i) => i.digest).join(',')}]`);
    return { digest, kind, items };
  }
  if (kind === 'object') {
    const entries = Object.keys(value as object)
      .sort()
      .map((k) => [k, buildDigest((value as Record<string, JsonValue>)[k], hash)] as [string, DigestNode]);
    const digest = hash(
      `obj{${entries.map(([k, n]) => `${JSON.stringify(k)}:${n.digest}`).join(',')}}`,
    );
    return { digest, kind, entries };
  }
  return { digest: hash(`lit:${JSON.stringify(value)}`), kind, value: value as never };
}

/** 结构化内容比较（对象键无序）。用于摘要相等时的碰撞复核。 */
export function contentEqual(a: DigestNode, b: DigestNode): boolean {
  if (a.kind !== b.kind) return false;
  switch (a.kind) {
    case 'array': {
      const ai = a.items!;
      const bi = b.items!;
      if (ai.length !== bi.length) return false;
      return ai.every((n, i) => contentEqual(n, bi[i]));
    }
    case 'object': {
      const ae = a.entries!;
      const be = b.entries!;
      if (ae.length !== be.length) return false;
      return ae.every(([k, n], i) => k === be[i][0] && contentEqual(n, be[i][1]));
    }
    default:
      return Object.is(a.value, b.value);
  }
}

/**
 * 摘要等价判定：摘要不等则必不等；摘要相等时做碰撞复核，
 * 即使哈希函数被强制碰撞也能发现不同内容。
 */
export function digestsEqual(a: DigestNode, b: DigestNode): boolean {
  if (a.digest !== b.digest) return false;
  return contentEqual(a, b);
}

/** 两个 JSON 值内容是否相等（对象键无序、数组有序）。 */
export function valuesEqual(a: JsonValue, b: JsonValue, hash: HashFn = defaultHash): boolean {
  return digestsEqual(buildDigest(a, hash), buildDigest(b, hash));
}

/** 规范化：递归按键排序对象。幂等，且规范化前后摘要一致。 */
export function normalizeTree(v: JsonValue): JsonValue {
  if (Array.isArray(v)) return v.map(normalizeTree);
  if (isObject(v)) {
    const out: Record<string, JsonValue> = {};
    for (const k of Object.keys(v).sort()) out[k] = normalizeTree(v[k]);
    return out;
  }
  return v;
}
