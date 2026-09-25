import { ChunkRef, deepEqual, formatPath, isObject, normalize } from './json.ts';
import type { ChunkReader, Json, Path } from './json.ts';
import { buildDigest, elementKey } from './digest.ts';
import type { DigestNode, DigestOptions } from './digest.ts';

export interface DiffOptions extends DigestOptions {
  /** When true, subtrees with equal digests are still content-compared (collision review). */
  verifyCollisions?: boolean;
  /** Chunk reader used to resolve ChunkRef nodes lazily. */
  reader?: ChunkReader;
}

export interface Change {
  kind: 'set' | 'remove' | 'move';
  /** Target-side path (for 'move', the destination). */
  path: Path;
  before?: Json | ChunkRef;
  after?: Json | ChunkRef;
  /** Source path for moves. */
  from?: Path;
  /** True when found only by content re-check despite equal digests. */
  collision?: boolean;
  /** Set whose content already existed at this base path (copy, not move). */
  copiedFrom?: Path;
}

export interface SourceInfo {
  source: Path;
  kind: 'same' | 'move' | 'copy';
}

export interface DiffResult {
  changes: Change[];
  /** Maps a target path (formatPath key) to its provenance in the base tree. */
  sourceMap: Map<string, SourceInfo>;
}

export function diff(base: Json | ChunkRef, target: Json | ChunkRef, opts: DiffOptions = {}): DiffResult {
  const reader = opts.reader;
  const changes: Change[] = [];
  const sourceMap = new Map<string, SourceInfo>();

  const resolve = (node: Json | ChunkRef): Json =>
    node instanceof ChunkRef ? reader!.get(node.id) : node;
  const digestNodeOf = (node: Json | ChunkRef, path: Path): DigestNode =>
    node instanceof ChunkRef ? reader!.nodeOf(node.id) : buildDigest(node, opts, reader, path);

  // Index base digests -> paths (does not read chunk contents; stops at chunk boundaries).
  const baseDigestIndex = new Map<string, Path[]>();
  const indexDigests = (node: Json | ChunkRef, path: Path): void => {
    const dn = digestNodeOf(node, path);
    const list = baseDigestIndex.get(dn.digest);
    if (list) list.push(path);
    else baseDigestIndex.set(dn.digest, [path]);
    if (node instanceof ChunkRef) return;
    if (Array.isArray(node)) node.forEach((el, i) => indexDigests(el, [...path, i]));
    else if (isObject(node)) for (const k of Object.keys(node)) indexDigests(node[k], [...path, k]);
  };
  indexDigests(base, []);

  const walkIdArray = (bv: Json[], tv: Json[], path: Path): void => {
    const idKey = opts.idKey ?? 'id';
    const bKeys = bv.map((el, i) => elementKey(el, i, 'id', idKey, path));
    const tKeys = tv.map((el, i) => elementKey(el, i, 'id', idKey, path));
    const bIndex = new Map(bKeys.map((k, i) => [k, i] as const));
    const tIndex = new Map(tKeys.map((k, i) => [k, i] as const));
    for (const [k, i] of bIndex) {
      if (!tIndex.has(k)) changes.push({ kind: 'remove', path: [...path, i], before: bv[i] });
    }
    for (const [k, j] of tIndex) {
      if (!bIndex.has(k)) changes.push({ kind: 'set', path: [...path, j], after: tv[j] });
    }
    for (const [k, i] of bIndex) {
      const j = tIndex.get(k);
      if (j === undefined) continue;
      if (i !== j) {
        changes.push({ kind: 'move', from: [...path, i], path: [...path, j] });
        sourceMap.set(formatPath([...path, j]), { source: [...path, i], kind: 'move' });
      }
      walk(bv[i], tv[j], [...path, j]);
    }
  };

  function walk(b: Json | ChunkRef, t: Json | ChunkRef, path: Path): void {
    const db = digestNodeOf(b, path);
    const dt = digestNodeOf(t, path);
    if (db.digest === dt.digest) {
      // Digest pruning: equal digests normally mean equal content.
      if (opts.verifyCollisions) {
        const bv = resolve(b);
        const tv = resolve(t);
        if (!deepEqual(normalize(bv), normalize(tv))) {
          changes.push({ kind: 'set', path, before: bv, after: tv, collision: true });
          return;
        }
      }
      sourceMap.set(formatPath(path), { source: path, kind: 'same' });
      return;
    }
    const bv = resolve(b);
    const tv = resolve(t);
    if (isObject(bv) && isObject(tv)) {
      const keys = new Set([...Object.keys(bv), ...Object.keys(tv)]);
      for (const k of keys) {
        const hasB = Object.prototype.hasOwnProperty.call(bv, k);
        const hasT = Object.prototype.hasOwnProperty.call(tv, k);
        if (hasB && hasT) walk(bv[k], tv[k], [...path, k]);
        else if (hasB) changes.push({ kind: 'remove', path: [...path, k], before: bv[k] });
        else changes.push({ kind: 'set', path: [...path, k], after: tv[k] });
      }
      return;
    }
    if (Array.isArray(bv) && Array.isArray(tv)) {
      const mode = opts.arrayMode?.(path) ?? 'position';
      if (mode === 'id') {
        walkIdArray(bv, tv, path);
        return;
      }
      const shared = Math.min(bv.length, tv.length);
      for (let i = 0; i < shared; i++) walk(bv[i], tv[i], [...path, i]);
      for (let i = shared; i < bv.length; i++) changes.push({ kind: 'remove', path: [...path, i], before: bv[i] });
      for (let i = shared; i < tv.length; i++) changes.push({ kind: 'set', path: [...path, i], after: tv[i] });
      return;
    }
    changes.push({ kind: 'set', path, before: bv, after: tv });
  }

  walk(base, target, []);

  // Post-pass: pair remove+set with identical digests into moves; a set whose content
  // still exists in the base is a copy, never a move.
  const removes = changes.filter((c) => c.kind === 'remove');
  const consumed = new Set<Change>();
  const replacements = new Map<Change, Change>();
  for (const c of changes) {
    if (c.kind !== 'set') continue;
    const d = digestNodeOf(c.after!, c.path).digest;
    const match = removes.find(
      (r) => !consumed.has(r) && digestNodeOf(r.before!, r.path).digest === d,
    );
    if (match) {
      consumed.add(match);
      replacements.set(c, { kind: 'move', path: c.path, from: match.path, before: match.before, after: c.after });
      sourceMap.set(formatPath(c.path), { source: match.path, kind: 'move' });
    } else {
      const sources = baseDigestIndex.get(d);
      if (sources && sources.length > 0) {
        c.copiedFrom = sources[0];
        sourceMap.set(formatPath(c.path), { source: sources[0], kind: 'copy' });
      }
    }
  }
  const finalChanges: Change[] = [];
  for (const c of changes) {
    if (consumed.has(c)) continue;
    finalChanges.push(replacements.get(c) ?? c);
  }

  return { changes: finalChanges, sourceMap };
}
