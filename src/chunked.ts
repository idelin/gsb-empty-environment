import { buildDigest, contentEqual, valuesEqual } from './digest.ts';
import { detectMoves, elementIdOf, arrayIdMap, type DiffOp } from './diff.ts';
import { defaultHash, type HashFn } from './hash.ts';
import { isObject, pathKey, type JsonValue, type Path } from './types.ts';

/**
 * 分块存储：模拟大树按块载入。
 * 深度为 blockDepth 整数倍的子树各为一块；manifest 保存每块摘要，
 * 摘要比对不读块，只有摘要不等（或碰撞复核）才真正 readBlock。
 */
export class ChunkStore {
  readonly root: JsonValue;
  readonly blockDepth: number;
  readonly hash: HashFn;
  private readonly blocks = new Map<string, JsonValue>();
  private readonly manifest = new Map<string, string>();
  /** 实际读取块计数。 */
  reads = 0;

  private constructor(root: JsonValue, blockDepth: number, hash: HashFn) {
    this.root = root;
    this.blockDepth = blockDepth;
    this.hash = hash;
    const walk = (v: JsonValue, path: Path): void => {
      if (path.length % blockDepth === 0) {
        const key = pathKey(path);
        this.blocks.set(key, v);
        this.manifest.set(key, buildDigest(v, hash).digest);
      }
      if (Array.isArray(v)) v.forEach((c, i) => walk(c, [...path, i]));
      else if (isObject(v)) for (const k of Object.keys(v)) walk(v[k], [...path, k]);
    };
    walk(root, []);
  }

  static fromTree(root: JsonValue, blockDepth = 2, hash: HashFn = defaultHash): ChunkStore {
    return new ChunkStore(root, blockDepth, hash);
  }

  get totalBlocks(): number {
    return this.blocks.size;
  }

  hasBlock(path: Path): boolean {
    return this.manifest.has(pathKey(path));
  }

  /** 块摘要：只查 manifest，不计读取。 */
  digestAt(path: Path): string | undefined {
    return this.manifest.get(pathKey(path));
  }

  /** 真正载入一块（计入读取统计）。 */
  readBlock(path: Path): JsonValue {
    const v = this.blocks.get(pathKey(path));
    if (v === undefined) throw new Error(`no block at ${pathKey(path)}`);
    this.reads++;
    return v;
  }
}

export interface ChunkedDiffOptions {
  hash?: HashFn;
  idKey?: string;
  /** 摘要相等时是否读块做碰撞复核（默认否，信任摘要剪枝）。 */
  verifyCollisions?: boolean;
}

/** 分块差异：沿摘要不等的块递归，统计实际读取块数以验证摘要裁剪。 */
export function diffChunked(a: ChunkStore, b: ChunkStore, opts: ChunkedDiffOptions = {}): DiffOp[] {
  const hash = opts.hash ?? a.hash;
  const idKey = opts.idKey ?? 'id';
  const ops: DiffOp[] = [];

  const valueAt = (store: ChunkStore, path: Path): JsonValue => {
    if (store.hasBlock(path)) return store.readBlock(path);
    // 非块根：从最近祖先块内取值（祖先块已被读取过，这里直接定位，不重复计数）。
    let cur: JsonValue = store.root;
    for (const seg of path) cur = (cur as never)[seg];
    return cur;
  };

  const visit = (path: Path): void => {
    const da = a.digestAt(path);
    const db = b.digestAt(path);
    if (da !== undefined && da === db) {
      if (!opts.verifyCollisions) return; // 摘要剪枝：不读块
      const va = a.readBlock(path);
      const vb = b.readBlock(path);
      if (contentEqual(buildDigest(va, hash), buildDigest(vb, hash))) return; // 碰撞复核通过
      diffWithin(va, vb, path);
      return;
    }
    diffWithin(valueAt(a, path), valueAt(b, path), path);
  };

  const diffWithin = (va: JsonValue, vb: JsonValue, path: Path): void => {
    if (valuesEqual(va, vb, hash)) return;
    const aArr = Array.isArray(va);
    const bArr = Array.isArray(vb);
    if (aArr !== bArr || (!aArr && !isObject(va)) || (!bArr && !isObject(vb))) {
      ops.push({ op: 'set', path, value: vb, oldValue: va });
      return;
    }
    if (isObject(va) && isObject(vb)) {
      for (const k of Object.keys(va)) {
        if (!(k in vb)) ops.push({ op: 'remove', path: [...path, k], oldValue: va[k] });
      }
      for (const k of Object.keys(vb)) {
        if (!(k in va)) ops.push({ op: 'set', path: [...path, k], value: vb[k] });
      }
      for (const k of Object.keys(va)) {
        if (k in vb) child(va[k], vb[k], [...path, k]);
      }
      return;
    }
    // 数组：块内按稳定 ID 匹配（有 id 时），否则按位置。
    const aa = va as JsonValue[];
    const bb = vb as JsonValue[];
    const hasIds = bb.some((el) => elementIdOf(el, idKey) !== undefined) || aa.some((el) => elementIdOf(el, idKey) !== undefined);
    if (hasIds) {
      const aIds = arrayIdMap(aa, idKey, path);
      const bIds = arrayIdMap(bb, idKey, path);
      const removed: number[] = [];
      for (const [id, ai] of aIds) if (!bIds.has(id)) removed.push(ai);
      removed.sort((x, y) => y - x);
      for (const i of removed) ops.push({ op: 'remove', path: [...path, i], oldValue: aa[i] });
      const added: number[] = [];
      for (const [id, bi] of bIds) if (!aIds.has(id)) added.push(bi);
      added.sort((x, y) => x - y);
      for (const i of added) ops.push({ op: 'insert', path: [...path, i], value: bb[i] });
      for (const [id, ai] of aIds) {
        const bi = bIds.get(id);
        if (bi !== undefined) child(aa[ai], bb[bi], [...path, bi]);
      }
      return;
    }
    const n = Math.min(aa.length, bb.length);
    const tailRemoves: DiffOp[] = [];
    for (let i = n; i < aa.length; i++) tailRemoves.push({ op: 'remove', path: [...path, i], oldValue: aa[i] });
    ops.push(...tailRemoves.reverse());
    for (let i = n; i < bb.length; i++) ops.push({ op: 'insert', path: [...path, i], value: bb[i] });
    for (let i = 0; i < n; i++) child(aa[i], bb[i], [...path, i]);
  };

  const child = (va: JsonValue, vb: JsonValue, path: Path): void => {
    if (a.hasBlock(path) && b.hasBlock(path)) visit(path); // 块边界：摘要剪枝
    else diffWithin(va, vb, path);
  };

  visit([]);
  return detectMoves(ops, hash);
}
