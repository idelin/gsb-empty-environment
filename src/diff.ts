import { buildDigest, digestsEqual, type DigestNode } from './digest.ts';
import { DuplicateArrayIdError } from './errors.ts';
import { defaultHash, type HashFn } from './hash.ts';
import { isObject, type JsonValue, type Path } from './types.ts';

/** 原子补丁操作。set/insert/remove 携带 oldValue 以便求逆。 */
export type DiffOp =
  | { op: 'set'; path: Path; value: JsonValue; oldValue?: JsonValue }
  | { op: 'insert'; path: Path; value: JsonValue }
  | { op: 'remove'; path: Path; oldValue: JsonValue }
  | { op: 'move'; from: Path; to: Path };

export type ArrayMode = 'position' | 'id';

export interface DiffOptions {
  hash?: HashFn;
  /** 数组匹配模式：稳定 ID 或显式位置。 */
  arrayMode?: ArrayMode;
  /** 稳定 ID 的键名，默认 'id'。 */
  idKey?: string;
}

interface Ctx {
  hash: HashFn;
  arrayMode: ArrayMode;
  idKey: string;
  ops: DiffOp[];
}

export function elementIdOf(el: JsonValue, idKey: string): string | undefined {
  if (!isObject(el)) return undefined;
  const raw = el[idKey];
  if (typeof raw === 'string') return raw;
  if (typeof raw === 'number') return String(raw);
  return undefined;
}

/** 提取数组元素稳定 ID；重复 ID 抛错并定位到数组路径。 */
export function arrayIdMap(arr: JsonValue[], idKey: string, path: Path): Map<string, number> {
  const map = new Map<string, number>();
  arr.forEach((el, i) => {
    const id = elementIdOf(el, idKey);
    if (id === undefined) return;
    if (map.has(id)) throw new DuplicateArrayIdError(id, path);
    map.set(id, i);
  });
  return map;
}

/** 由源顺序与目标顺序（同一批 id）模拟出 move 操作序列。 */
function computeMoves(fromIds: string[], toIds: string[], basePath: Path): DiffOp[] {
  const cur = fromIds.slice();
  const ops: DiffOp[] = [];
  for (let i = 0; i < toIds.length; i++) {
    const j = cur.indexOf(toIds[i]);
    if (j === -1) continue;
    if (j !== i) {
      ops.push({ op: 'move', from: [...basePath, j], to: [...basePath, i] });
      cur.splice(j, 1);
      cur.splice(i, 0, toIds[i]);
    }
  }
  return ops;
}

function diffArrayId(a: JsonValue[], b: JsonValue[], na: DigestNode, nb: DigestNode, path: Path, ctx: Ctx): void {
  const aIds = arrayIdMap(a, ctx.idKey, path);
  const bIds = arrayIdMap(b, ctx.idKey, path);
  const aIdless = a.map((el, i) => (elementIdOf(el, ctx.idKey) === undefined ? i : -1)).filter((i) => i >= 0);
  const bIdless = b.map((el, i) => (elementIdOf(el, ctx.idKey) === undefined ? i : -1)).filter((i) => i >= 0);

  const removedIdx: number[] = [];
  const matched: Array<[number, number]> = [];
  for (const [id, ai] of aIds) {
    const bi = bIds.get(id);
    if (bi === undefined) removedIdx.push(ai);
    else matched.push([ai, bi]);
  }
  const addedIdx: number[] = [];
  for (const [id, bi] of bIds) if (!aIds.has(id)) addedIdx.push(bi);

  // 无 id 元素按出现次序配对。
  const idlessMatched: Array<[number, number]> = [];
  const n = Math.min(aIdless.length, bIdless.length);
  for (let k = 0; k < n; k++) idlessMatched.push([aIdless[k], bIdless[k]]);
  for (let k = n; k < aIdless.length; k++) removedIdx.push(aIdless[k]);
  for (let k = n; k < bIdless.length; k++) addedIdx.push(bIdless[k]);

  // 1) 删除：按源下标降序，保证下标有效。
  removedIdx.sort((x, y) => y - x);
  for (const i of removedIdx) ctx.ops.push({ op: 'remove', path: [...path, i], oldValue: a[i] });

  // 2) 移动：在“删除后 / 新增前”的 id 序列上模拟。
  const removedSet = new Set(removedIdx);
  const fromIds = a.flatMap((el, i) => {
    const id = elementIdOf(el, ctx.idKey);
    return id !== undefined && !removedSet.has(i) && bIds.has(id) ? [id] : [];
  });
  const addedSet = new Set(addedIdx);
  const toIds = b.flatMap((el, i) => {
    const id = elementIdOf(el, ctx.idKey);
    return id !== undefined && !addedSet.has(i) && aIds.has(id) ? [id] : [];
  });
  ctx.ops.push(...computeMoves(fromIds, toIds, path));

  // 3) 新增：按目标下标升序插入。
  addedIdx.sort((x, y) => x - y);
  for (const i of addedIdx) ctx.ops.push({ op: 'insert', path: [...path, i], value: b[i] });

  // 4) 配对元素内容递归（路径用目标下标，此时数组已重排+插入完毕）。
  for (const [ai, bi] of [...matched, ...idlessMatched]) {
    diffValue(a[ai], b[bi], na.items![ai], nb.items![bi], [...path, bi], ctx);
  }
}

function diffArrayPosition(a: JsonValue[], b: JsonValue[], na: DigestNode, nb: DigestNode, path: Path, ctx: Ctx): void {
  const n = Math.min(a.length, b.length);
  const tail: DiffOp[] = [];
  for (let i = n; i < a.length; i++) tail.push({ op: 'remove', path: [...path, i], oldValue: a[i] });
  tail.reverse(); // 降序删除
  ctx.ops.push(...tail);
  for (let i = n; i < b.length; i++) ctx.ops.push({ op: 'insert', path: [...path, i], value: b[i] });
  for (let i = 0; i < n; i++) diffValue(a[i], b[i], na.items![i], nb.items![i], [...path, i], ctx);
}

function diffValue(a: JsonValue, b: JsonValue, na: DigestNode, nb: DigestNode, path: Path, ctx: Ctx): void {
  if (digestsEqual(na, nb)) return; // 摘要剪枝 + 碰撞复核
  if (na.kind !== nb.kind || na.kind === 'null' || na.kind === 'boolean' || na.kind === 'number' || na.kind === 'string') {
    ctx.ops.push({ op: 'set', path, value: b, oldValue: a });
    return;
  }
  if (na.kind === 'object') {
    const ao = a as Record<string, JsonValue>;
    const bo = b as Record<string, JsonValue>;
    for (const k of Object.keys(ao)) {
      if (!(k in bo)) ctx.ops.push({ op: 'remove', path: [...path, k], oldValue: ao[k] });
    }
    for (const k of Object.keys(bo)) {
      if (!(k in ao)) ctx.ops.push({ op: 'set', path: [...path, k], value: bo[k] });
    }
    for (const k of Object.keys(ao)) {
      if (k in bo) {
        const ia = na.entries!.find(([ek]) => ek === k)![1];
        const ib = nb.entries!.find(([ek]) => ek === k)![1];
        diffValue(ao[k], bo[k], ia, ib, [...path, k], ctx);
      }
    }
    return;
  }
  // array
  const aa = a as JsonValue[];
  const ba = b as JsonValue[];
  if (ctx.arrayMode === 'id') diffArrayId(aa, ba, na, nb, path, ctx);
  else diffArrayPosition(aa, ba, na, nb, path, ctx);
}

/**
 * 移动检测：被删除子树与新增子树摘要一致（含碰撞复核）时改写为 move。
 * 复制不会产生 remove，因此不会被误判为移动。
 */
export function detectMoves(ops: DiffOp[], hash: HashFn = defaultHash): DiffOp[] {
  const removes = ops
    .map((o, i) => ({ o, i }))
    .filter(({ o }) => o.op === 'remove') as Array<{ o: Extract<DiffOp, { op: 'remove' }>; i: number }>;
  const adds = ops
    .map((o, i) => ({ o, i }))
    .filter(({ o }) => (o.op === 'set' && o.oldValue === undefined) || o.op === 'insert') as Array<{
    o: Extract<DiffOp, { op: 'set' | 'insert' }>;
    i: number;
  }>;
  const consumed = new Set<number>();
  const replaced = new Map<number, DiffOp>();
  for (const r of removes) {
    if (consumed.has(r.i)) continue;
    const rd = buildDigest(r.o.oldValue, hash);
    for (const a of adds) {
      if (consumed.has(a.i)) continue;
      if (digestsEqual(rd, buildDigest(a.o.value, hash))) {
        replaced.set(a.i, { op: 'move', from: r.o.path, to: a.o.path });
        consumed.add(r.i);
        consumed.add(a.i);
        break;
      }
    }
  }
  const out: DiffOp[] = [];
  ops.forEach((o, i) => {
    if (replaced.has(i)) out.push(replaced.get(i)!);
    else if (!consumed.has(i)) out.push(o);
  });
  return out;
}

/** 计算 a → b 的差异操作序列（含移动检测）。 */
export function diffTrees(a: JsonValue, b: JsonValue, opts: DiffOptions = {}): DiffOp[] {
  const ctx: Ctx = {
    hash: opts.hash ?? defaultHash,
    arrayMode: opts.arrayMode ?? 'position',
    idKey: opts.idKey ?? 'id',
    ops: [],
  };
  diffValue(a, b, buildDigest(a, ctx.hash), buildDigest(b, ctx.hash), [], ctx);
  return detectMoves(ctx.ops, ctx.hash);
}
