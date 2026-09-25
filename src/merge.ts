import { buildDigest, contentEqual, valuesEqual, type DigestNode } from './digest.ts';
import { diffTrees, elementIdOf, arrayIdMap, type ArrayMode, type DiffOp } from './diff.ts';
import { defaultHash, type HashFn } from './hash.ts';
import { deepClone, isObject, type JsonValue, type Path } from './types.ts';

/** 冲突：三方值（undefined 表示该侧不存在）。 */
export interface Conflict {
  path: Path;
  base?: JsonValue;
  left?: JsonValue;
  right?: JsonValue;
  reason: string;
}

/** 来源映射：输出节点 → 来源侧与来源路径。 */
export interface SourceEntry {
  outPath: Path;
  side: 'base' | 'left' | 'right';
  path: Path;
  relation: 'kept' | 'modified' | 'added' | 'moved' | 'copied';
}

export interface MergeOptions {
  hash?: HashFn;
  arrayMode?: ArrayMode;
  idKey?: string;
}

export interface MergeResult {
  result: JsonValue;
  /** 基线 → 合并结果的原子补丁（含未解决冲突的回退结果）。 */
  patch: { ops: DiffOp[] };
  conflicts: Conflict[];
  sources: SourceEntry[];
}

const ABSENT: unique symbol = Symbol('absent');
type Maybe = JsonValue | typeof ABSENT;

interface SrcPaths {
  base?: Path;
  left?: Path;
  right?: Path;
}

interface MCtx {
  hash: HashFn;
  arrayMode: ArrayMode;
  idKey: string;
  conflicts: Conflict[];
  sources: SourceEntry[];
  /** 基线摘要索引：digest → 出现位置，用于复制检测（含碰撞复核）。 */
  baseIndex: Map<string, Array<{ path: Path; node: DigestNode }>>;
}

function buildIndex(root: JsonValue, hash: HashFn): MCtx['baseIndex'] {
  const index: MCtx['baseIndex'] = new Map();
  const walk = (v: JsonValue, path: Path): void => {
    const node = buildDigest(v, hash);
    const list = index.get(node.digest) ?? [];
    list.push({ path, node });
    index.set(node.digest, list);
    if (Array.isArray(v)) v.forEach((c, i) => walk(c, [...path, i]));
    else if (isObject(v)) for (const k of Object.keys(v)) walk(v[k], [...path, k]);
  };
  walk(root, []);
  return index;
}

function conflict(ctx: MCtx, path: Path, base: Maybe, left: Maybe, right: Maybe, reason: string): void {
  ctx.conflicts.push({
    path,
    base: base === ABSENT ? undefined : deepClone(base),
    left: left === ABSENT ? undefined : deepClone(left),
    right: right === ABSENT ? undefined : deepClone(right),
    reason,
  });
}

/** 新增子树若与基线中既有子树内容一致，则为复制而非移动。 */
function isCopyOfBase(ctx: MCtx, value: JsonValue): boolean {
  const node = buildDigest(value, ctx.hash);
  const candidates = ctx.baseIndex.get(node.digest);
  return candidates !== undefined && candidates.some((c) => contentEqual(c.node, node));
}

function recordAdded(ctx: MCtx, side: 'left' | 'right', value: JsonValue, outPath: Path, srcPath: Path): void {
  ctx.sources.push({
    outPath,
    side,
    path: srcPath,
    relation: isCopyOfBase(ctx, value) ? 'copied' : 'added',
  });
}

function childSrc(src: SrcPaths, seg: string | number): SrcPaths {
  return {
    base: src.base ? [...src.base, seg] : undefined,
    left: src.left ? [...src.left, seg] : undefined,
    right: src.right ? [...src.right, seg] : undefined,
  };
}

function mergeNode(b: Maybe, l: Maybe, r: Maybe, path: Path, ctx: MCtx, src: SrcPaths): Maybe {
  const lA = l === ABSENT;
  const rA = r === ABSENT;
  const bA = b === ABSENT;

  if (lA && rA) return ABSENT;
  if (lA || rA) {
    const presentSide: 'left' | 'right' = lA ? 'right' : 'left';
    const present = (lA ? r : l) as JsonValue;
    const presentSrc = (lA ? src.right : src.left) ?? path;
    if (bA) {
      recordAdded(ctx, presentSide, present, path, presentSrc);
      return present;
    }
    if (valuesEqual(b as JsonValue, present, ctx.hash)) return ABSENT; // 一侧删除，另一侧未动
    // 一侧删除、另一侧修改 → 冲突（回退保留修改侧）。
    if (lA) conflict(ctx, path, b, ABSENT, r, 'delete-vs-modify');
    else conflict(ctx, path, b, l, ABSENT, 'delete-vs-modify');
    ctx.sources.push({ outPath: path, side: presentSide, path: presentSrc, relation: 'modified' });
    return present;
  }

  const lv = l as JsonValue;
  const rv = r as JsonValue;
  if (valuesEqual(lv, rv, ctx.hash)) {
    if (!bA && valuesEqual(b as JsonValue, lv, ctx.hash)) {
      ctx.sources.push({ outPath: path, side: 'base', path: src.base ?? path, relation: 'kept' });
    } else if (bA) {
      recordAdded(ctx, 'left', lv, path, src.left ?? path);
    } else {
      ctx.sources.push({ outPath: path, side: 'left', path: src.left ?? path, relation: 'modified' });
    }
    return lv;
  }
  if (!bA && valuesEqual(b as JsonValue, lv, ctx.hash)) {
    return takeSide(b as JsonValue, rv, lv, 'right', path, ctx, src);
  }
  if (!bA && valuesEqual(b as JsonValue, rv, ctx.hash)) {
    return takeSide(b as JsonValue, lv, rv, 'left', path, ctx, src);
  }

  // 双方都改了（或基线不存在且双方添加不同内容）。
  if (isObject(lv) && isObject(rv) && (bA || isObject(b))) {
    return mergeObject(bA ? ABSENT : b, lv, rv, path, ctx, src);
  }
  if (Array.isArray(lv) && Array.isArray(rv) && (bA || Array.isArray(b))) {
    return mergeArray(bA ? ABSENT : b, lv, rv, path, ctx, src);
  }
  conflict(ctx, path, b, lv, rv, bA ? 'both-added' : 'both-modified');
  ctx.sources.push({ outPath: path, side: 'left', path: src.left ?? path, relation: 'modified' });
  return lv; // 回退取左侧
}

/** 仅一侧变更：结构上可递归则下钻以记录细粒度来源，否则整体记录 modified。 */
function takeSide(b: JsonValue, changed: JsonValue, mirror: JsonValue, side: 'left' | 'right', path: Path, ctx: MCtx, src: SrcPaths): JsonValue {
  const l = side === 'left' ? changed : mirror;
  const r = side === 'left' ? mirror : changed;
  if (isObject(changed) && isObject(b)) return mergeObject(b, l as Record<string, JsonValue>, r as Record<string, JsonValue>, path, ctx, src);
  if (Array.isArray(changed) && Array.isArray(b) && ctx.arrayMode === 'id') {
    return mergeArray(b, l as JsonValue[], r as JsonValue[], path, ctx, src);
  }
  ctx.sources.push({ outPath: path, side, path: (side === 'left' ? src.left : src.right) ?? path, relation: 'modified' });
  return changed;
}

function mergeObject(b: Maybe, l: Record<string, JsonValue>, r: Record<string, JsonValue>, path: Path, ctx: MCtx, src: SrcPaths): JsonValue {
  const bo = b !== ABSENT ? (b as Record<string, JsonValue>) : undefined;
  const keys = new Set([...Object.keys(l), ...Object.keys(r), ...(bo ? Object.keys(bo) : [])]);
  const out: Record<string, JsonValue> = {};
  for (const k of keys) {
    const bv: Maybe = bo && Object.prototype.hasOwnProperty.call(bo, k) ? bo[k] : ABSENT;
    const lv: Maybe = Object.prototype.hasOwnProperty.call(l, k) ? l[k] : ABSENT;
    const rv: Maybe = Object.prototype.hasOwnProperty.call(r, k) ? r[k] : ABSENT;
    const res = mergeNode(bv, lv, rv, [...path, k], ctx, childSrc(src, k));
    if (res !== ABSENT) out[k] = res;
  }
  return out;
}

interface IdState {
  id: string;
  bIdx?: number;
  lIdx?: number;
  rIdx?: number;
}

function mergeArray(b: Maybe, l: JsonValue[], r: JsonValue[], path: Path, ctx: MCtx, src: SrcPaths): JsonValue {
  if (ctx.arrayMode === 'position') {
    const ba = b !== ABSENT ? (b as JsonValue[]) : undefined;
    const n = Math.max(l.length, r.length, ba?.length ?? 0);
    const out: JsonValue[] = [];
    for (let i = 0; i < n; i++) {
      const bv: Maybe = ba && i < ba.length ? ba[i] : ABSENT;
      const lv: Maybe = i < l.length ? l[i] : ABSENT;
      const rv: Maybe = i < r.length ? r[i] : ABSENT;
      const res = mergeNode(bv, lv, rv, [...path, i], ctx, childSrc(src, i));
      if (res !== ABSENT) out.push(res);
    }
    return out;
  }

  // 稳定 ID 模式：无 id 元素按出现次序合成键配对。
  const ba = b !== ABSENT ? (b as JsonValue[]) : [];
  const idOfAt = (arr: JsonValue[], i: number): string => {
    const direct = elementIdOf(arr[i], ctx.idKey);
    if (direct !== undefined) return direct;
    let rank = 0;
    for (let k = 0; k < i; k++) if (elementIdOf(arr[k], ctx.idKey) === undefined) rank++;
    return `#noid:${rank}`;
  };
  // 重复 id 校验（定位到数组路径）。
  if (b !== ABSENT) arrayIdMap(ba, ctx.idKey, src.base ?? path);
  arrayIdMap(l, ctx.idKey, src.left ?? path);
  arrayIdMap(r, ctx.idKey, src.right ?? path);

  const states = new Map<string, IdState>();
  const collect = (arr: JsonValue[], side: 'bIdx' | 'lIdx' | 'rIdx'): void => {
    arr.forEach((_, i) => {
      const id = idOfAt(arr, i);
      const st = states.get(id) ?? { id };
      st[side] = i;
      states.set(id, st);
    });
  };
  collect(ba, 'bIdx');
  collect(l, 'lIdx');
  collect(r, 'rIdx');

  // 存活判定（删除 vs 修改冲突的回退是保留修改侧，因此仍存活）。
  const surviving = new Set<string>();
  for (const st of states.values()) {
    const inL = st.lIdx !== undefined;
    const inR = st.rIdx !== undefined;
    if (!inL && !inR) continue;
    if (inL && inR || st.bIdx === undefined) {
      surviving.add(st.id);
      continue;
    }
    const present = inL ? l[st.lIdx!] : r[st.rIdx!];
    if (!valuesEqual(ba[st.bIdx], present, ctx.hash)) surviving.add(st.id); // 删除 vs 修改 → 保留修改侧
  }

  const orderOf = (arr: JsonValue[]): string[] =>
    arr.flatMap((_, i) => {
      const id = idOfAt(arr, i);
      return surviving.has(id) ? [id] : [];
    });
  const baseOrder = orderOf(ba);
  const leftOrder = orderOf(l);
  const rightOrder = orderOf(r);
  const eqSeq = (x: string[], y: string[]): boolean => x.length === y.length && x.every((v, i) => v === y[i]);
  const shared = new Set(leftOrder.filter((id) => rightOrder.includes(id)));
  const relL = leftOrder.filter((id) => shared.has(id));
  const relR = rightOrder.filter((id) => shared.has(id));
  const relB = baseOrder.filter((id) => shared.has(id));
  const weave = (primary: string[], secondary: string[]): string[] => {
    const out = primary.slice();
    for (const id of secondary) {
      if (out.includes(id)) continue;
      const after = secondary.slice(secondary.indexOf(id) + 1).find((x) => out.includes(x));
      if (after === undefined) out.push(id);
      else out.splice(out.indexOf(after), 0, id);
    }
    return out;
  };
  // 顺序仲裁：与基线一致的一侧视为未重排，取另一侧顺序；双方都重排且不一致才冲突。
  let finalOrder: string[];
  if (eqSeq(relL, relR) || eqSeq(relR, relB)) finalOrder = weave(leftOrder, rightOrder);
  else if (eqSeq(relL, relB)) finalOrder = weave(rightOrder, leftOrder);
  else {
    conflict(ctx, path, b, l, r, 'array-order');
    finalOrder = weave(leftOrder, rightOrder);
  }

  const out: JsonValue[] = [];
  finalOrder.forEach((id, i) => {
    const st = states.get(id)!;
    const bv: Maybe = st.bIdx !== undefined ? ba[st.bIdx] : ABSENT;
    const lv: Maybe = st.lIdx !== undefined ? l[st.lIdx] : ABSENT;
    const rv: Maybe = st.rIdx !== undefined ? r[st.rIdx] : ABSENT;
    const childPaths: SrcPaths = {
      base: src.base && st.bIdx !== undefined ? [...src.base, st.bIdx] : undefined,
      left: src.left && st.lIdx !== undefined ? [...src.left, st.lIdx] : undefined,
      right: src.right && st.rIdx !== undefined ? [...src.right, st.rIdx] : undefined,
    };
    const res = mergeNode(bv, lv, rv, [...path, i], ctx, childPaths);
    if (res === ABSENT) return;
    if (st.bIdx !== undefined && st.bIdx !== i && src.base) {
      // 身份保留但位置变化：记录移动（来源为基线旧下标）。
      ctx.sources.push({ outPath: [...path, i], side: 'base', path: [...src.base, st.bIdx], relation: 'moved' });
    }
    out.push(res);
  });
  return out;
}

/** 三方合并：输出结果树、基线→结果的原子补丁、冲突（三方值）与来源映射。 */
export function merge3(base: JsonValue, left: JsonValue, right: JsonValue, opts: MergeOptions = {}): MergeResult {
  const hash = opts.hash ?? defaultHash;
  const ctx: MCtx = {
    hash,
    arrayMode: opts.arrayMode ?? 'position',
    idKey: opts.idKey ?? 'id',
    conflicts: [],
    sources: [],
    baseIndex: buildIndex(base, hash),
  };
  const merged = mergeNode(base, left, right, [], ctx, { base: [], left: [], right: [] });
  const result = merged === ABSENT ? null : merged;
  const patch = { ops: diffTrees(base, result, { hash, arrayMode: ctx.arrayMode, idKey: ctx.idKey }) };
  return { result, patch, conflicts: ctx.conflicts, sources: ctx.sources };
}
