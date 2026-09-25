/**
 * Versioned tree store on top of the path-record stream.
 *
 * Rebuild pipeline (records may arrive out of order):
 *   feed -> buffer in memory, spilling sorted runs to disk past a threshold
 *        -> k-way merge runs into one sorted stream
 *        -> validate while streaming (parent/child types, duplicates,
 *           reference integrity) and assemble + write the new index
 *        -> atomically publish: ROOT.json is swapped only after the index
 *           is durable, so a crash never exposes a half-written tree.
 *
 * Committed versions are immutable; prefix cursors keep reading the version
 * they were opened on, even across later commits.
 */
import { Buffer } from 'node:buffer';
import { randomUUID } from 'node:crypto';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { Assembler } from './assemble.ts';
import { BuildError } from './errors.ts';
import {
  canHaveChildren,
  DeltaEncoder,
  Op,
  RecordDecoder,
  type OpCode,
  type Rec,
} from './format.ts';
import {
  comparePaths,
  equalPaths,
  formatPath,
  fromArray,
  isPrefix,
  pathDepth,
  suffixArray,
  type Path,
  type Seg,
} from './path.ts';

const ROOT_FILE = 'ROOT.json';
const CHUNK = 1 << 16;

interface Manifest {
  current: string | null;
  versions: string[];
}

export interface StoreOptions {
  /** Max buffered records before spilling a sorted run to disk. */
  spillThreshold?: number;
}

export interface PrefixRow {
  path: Seg[]; // relative to the queried prefix
  op: OpCode;
  val?: string | number;
}

/** Stream records from a record-stream file in 64 KiB chunks. */
export function* fileRecords(file: string): Generator<Rec> {
  const fd = fs.openSync(file, 'r');
  const dec = new RecordDecoder();
  try {
    for (;;) {
      const chunk = Buffer.allocUnsafe(CHUNK);
      const n = fs.readSync(fd, chunk, 0, CHUNK, null);
      if (n === 0) break;
      yield* dec.push(chunk.subarray(0, n));
    }
    dec.finish();
  } finally {
    fs.closeSync(fd);
  }
}

function writeAll(fd: number, buf: Buffer): void {
  let off = 0;
  while (off < buf.length) off += fs.writeSync(fd, buf, off, buf.length - off);
}

function* mergeIterators(sources: Array<Iterator<Rec>>): Generator<Rec> {
  const heads = sources.map((it) => it.next());
  for (;;) {
    let best = -1;
    for (let i = 0; i < heads.length; i++) {
      if (heads[i].done) continue;
      if (best < 0 || comparePaths(heads[i].value.path, heads[best].value.path) < 0) best = i;
    }
    if (best < 0) return;
    yield heads[best].value as Rec;
    heads[best] = sources[best].next();
  }
}

/** Skip base records at-or-under any (sorted, disjoint) replacement root. */
function* skipUnderRoots(base: Iterable<Rec>, roots: Path[]): Generator<Rec> {
  let ri = 0;
  for (const rec of base) {
    while (
      ri < roots.length &&
      !isPrefix(roots[ri], rec.path) &&
      comparePaths(roots[ri], rec.path) < 0
    ) {
      ri++;
    }
    if (ri < roots.length && isPrefix(roots[ri], rec.path)) continue;
    yield rec;
  }
}

export class TreeStore {
  readonly hooks: { afterIndexWrite?: (id: string) => void } = {};
  readonly dir: string;
  readonly spillThreshold: number;
  private manifest: Manifest;
  private rootCache = new Map<string, unknown>();

  private constructor(dir: string, manifest: Manifest, spillThreshold: number) {
    this.dir = dir;
    this.spillThreshold = spillThreshold;
    this.manifest = manifest;
  }

  static open(dir: string, opts: StoreOptions = {}): TreeStore {
    fs.mkdirSync(dir, { recursive: true });
    const rootFile = path.join(dir, ROOT_FILE);
    let manifest: Manifest = { current: null, versions: [] };
    if (fs.existsSync(rootFile)) {
      manifest = JSON.parse(fs.readFileSync(rootFile, 'utf8')) as Manifest;
    }
    // Recovery: ROOT.json is the only source of truth. Anything not committed
    // there (orphan .idx, leftover .tmp/.run) is removed, never exposed.
    for (const f of fs.readdirSync(dir)) {
      if (f.endsWith('.tmp') || f.endsWith('.run')) {
        fs.unlinkSync(path.join(dir, f));
      } else if (f.endsWith('.idx') && !manifest.versions.includes(f.slice(0, -4))) {
        fs.unlinkSync(path.join(dir, f));
      }
    }
    return new TreeStore(dir, manifest, opts.spillThreshold ?? 100_000);
  }

  get currentVersion(): string | null {
    return this.manifest.current;
  }

  versions(): string[] {
    return [...this.manifest.versions];
  }

  hasVersion(id: string): boolean {
    return this.manifest.versions.includes(id);
  }

  beginBuild(): Builder {
    return new Builder(this);
  }

  idxPath(id: string): string {
    return path.join(this.dir, `${id}.idx`);
  }

  private writeManifest(m: Manifest): void {
    const tmp = path.join(this.dir, `${ROOT_FILE}.tmp`);
    fs.writeFileSync(tmp, JSON.stringify(m));
    fs.renameSync(tmp, path.join(this.dir, ROOT_FILE));
    this.manifest = m;
  }

  /**
   * Validate a sorted record stream, write the version index, then publish.
   * Throws BuildError (with the offending path) before anything is published.
   */
  commitSorted(recs: Iterable<Rec>, id: string): string {
    const asm = new Assembler();
    const enc = new DeltaEncoder();
    const tmp = `${this.idxPath(id)}.tmp`;
    const fd = fs.openSync(tmp, 'w');
    let prev: Rec | null = null;
    let tree: unknown;
    try {
      for (const rec of recs) {
        if (prev) {
          if (equalPaths(prev.path, rec.path)) {
            throw new BuildError('duplicate definition', rec.path);
          }
          if (
            !canHaveChildren(prev.op) &&
            pathDepth(rec.path) > pathDepth(prev.path) &&
            isPrefix(prev.path, rec.path)
          ) {
            throw new BuildError('child path appears under a scalar parent', rec.path);
          }
        }
        asm.push(rec);
        writeAll(fd, enc.accept(rec));
        prev = rec;
      }
      tree = asm.finish();
      fs.fsyncSync(fd);
      fs.closeSync(fd);
      fs.renameSync(tmp, this.idxPath(id));
    } catch (err) {
      try {
        fs.closeSync(fd);
      } catch {}
      try {
        fs.unlinkSync(tmp);
      } catch {}
      throw err;
    }
    // The index is durable; swapping the root pointer is the atomic commit.
    this.hooks.afterIndexWrite?.(id);
    this.writeManifest({ current: id, versions: [...this.manifest.versions, id] });
    this.rootCache.set(id, tree);
    return id;
  }

  private streamVersion(id: string): Generator<Rec> {
    if (!this.hasVersion(id)) throw new Error(`unknown version ${id}`);
    return fileRecords(this.idxPath(id));
  }

  getTree(version?: string): unknown {
    const id = version ?? this.manifest.current;
    if (!id) throw new Error('store has no committed version');
    if (!this.hasVersion(id)) throw new Error(`unknown version ${id}`);
    if (this.rootCache.has(id)) return this.rootCache.get(id);
    const asm = new Assembler();
    for (const rec of this.streamVersion(id)) asm.push(rec);
    const tree = asm.finish();
    this.rootCache.set(id, tree);
    return tree;
  }

  /**
   * Streaming prefix query over a committed version. Yields rows with paths
   * relative to `prefix`, without materializing the rest of the tree. The
   * returned generator reads the immutable version file, so it keeps seeing
   * the old version even if the store commits afterwards.
   */
  *queryPrefix(version: string, prefix: Seg[]): Generator<PrefixRow> {
    const pre = fromArray(prefix);
    const preDepth = prefix.length;
    let active = false;
    for (const rec of this.streamVersion(version)) {
      if (isPrefix(pre, rec.path)) {
        active = true;
        yield { path: suffixArray(rec.path, preDepth), op: rec.op, val: rec.val };
      } else if (active || comparePaths(rec.path, pre) > 0) {
        break; // sorted stream: the prefix subtree is contiguous, we passed it
      }
    }
  }

  /** Materialize just the subtree at `prefix`; undefined if it does not exist. */
  materializePrefix(version: string, prefix: Seg[]): unknown {
    const asm = new Assembler();
    let any = false;
    for (const row of this.queryPrefix(version, prefix)) {
      any = true;
      asm.push({ path: fromArray(row.path), op: row.op, val: row.val });
    }
    return any ? asm.finish() : undefined;
  }

  private lookup(version: string, segs: Seg[]): PrefixRow | null {
    for (const row of this.queryPrefix(version, segs)) {
      return row.path.length === 0 ? row : null;
    }
    return null;
  }

  /**
   * Apply a patch (records + Missing tombstones) on top of a base version and
   * commit the result as a new version. Each topmost patched path replaces the
   * whole subtree below it; tombstones delete their subtree. Validation
   * failures leave the store untouched.
   */
  applyPatch(baseVersion: string, patch: Rec[]): string {
    if (!this.hasVersion(baseVersion)) throw new Error(`unknown version ${baseVersion}`);
    const sorted = [...patch].sort((a, b) => comparePaths(a.path, b.path));
    const deduped: Rec[] = [];
    for (let i = 0; i < sorted.length; i++) {
      if (i + 1 < sorted.length && equalPaths(sorted[i].path, sorted[i + 1].path)) continue;
      deduped.push(sorted[i]); // last definition of a path wins
    }
    const roots: Path[] = [];
    for (const r of deduped) {
      const last = roots[roots.length - 1];
      if (last && pathDepth(r.path) > pathDepth(last) && isPrefix(last, r.path)) continue;
      roots.push(r.path);
    }
    const base = skipUnderRoots(this.streamVersion(baseVersion), roots);
    const additions = deduped.filter((r) => r.op !== Op.Missing);
    const merged = mergeIterators([base[Symbol.iterator](), additions[Symbol.iterator]()]);
    return this.commitSorted(merged, `v-${randomUUID()}`);
  }

  /**
   * Move the subtree at `from` to `to` as a minimal delta (one tombstone plus
   * the moved records), commit it, and return the new version together with
   * the inverse patch that restores the base version exactly.
   */
  moveSubtree(
    baseVersion: string,
    from: Seg[],
    to: Seg[],
  ): { version: string; patch: Rec[]; inverse: Rec[] } {
    if (!this.hasVersion(baseVersion)) throw new Error(`unknown version ${baseVersion}`);
    if (to.length === 0) throw new BuildError('cannot move a subtree to the root', null);
    const fromPath = fromArray(from);
    const toPath = fromArray(to);
    if (equalPaths(fromPath, toPath)) {
      throw new BuildError('move source and destination are the same', toPath);
    }
    if (pathDepth(toPath) > pathDepth(fromPath) && isPrefix(fromPath, toPath)) {
      throw new BuildError('cannot move a subtree into itself', toPath);
    }
    if (!this.lookup(baseVersion, from)) {
      throw new BuildError('move source does not exist', fromPath);
    }
    const parentSegs = to.slice(0, -1);
    const parent = this.lookup(baseVersion, parentSegs);
    if (!parent) throw new BuildError('move destination parent does not exist', fromArray(parentSegs));
    const lastSeg = to[to.length - 1];
    if (!canHaveChildren(parent.op)) {
      throw new BuildError('move destination parent is a scalar', fromArray(parentSegs));
    }
    if (parent.op === Op.Obj && lastSeg.t !== 'k') {
      throw new BuildError('destination parent is an object; a string key is required', toPath);
    }
    if (parent.op === Op.Arr && lastSeg.t !== 'i') {
      throw new BuildError('destination parent is an array; an index is required', toPath);
    }

    const srcRows = [...this.queryPrefix(baseVersion, from)];
    const oldDst = this.lookup(baseVersion, to) ? [...this.queryPrefix(baseVersion, to)] : null;

    const patch: Rec[] = [{ path: fromPath, op: Op.Missing }];
    for (const row of srcRows) {
      patch.push({ path: fromArray([...to, ...row.path]), op: row.op, val: row.val });
    }

    const inverse: Rec[] = [];
    if (oldDst) {
      for (const row of oldDst) inverse.push({ path: fromArray([...to, ...row.path]), op: row.op, val: row.val });
    } else {
      inverse.push({ path: toPath, op: Op.Missing });
    }
    for (const row of srcRows) {
      inverse.push({ path: fromArray([...from, ...row.path]), op: row.op, val: row.val });
    }

    const version = this.applyPatch(baseVersion, patch);
    return { version, patch, inverse };
  }
}

export class Builder {
  readonly id = `v-${randomUUID()}`;
  spills = 0;
  private store: TreeStore;
  private buf: Rec[] = [];
  private runs: string[] = [];
  private done = false;

  constructor(store: TreeStore) {
    this.store = store;
  }

  feed(rec: Rec): void {
    if (this.done) throw new Error('builder is already finished');
    this.buf.push(rec);
    if (this.buf.length >= this.store.spillThreshold) this.spill();
  }

  feedAll(recs: Iterable<Rec>): void {
    for (const rec of recs) this.feed(rec);
  }

  private spill(): void {
    if (this.buf.length === 0) return;
    this.buf.sort((a, b) => comparePaths(a.path, b.path));
    const file = path.join(this.store.dir, `spill-${this.id}-${this.spills++}.run`);
    const fd = fs.openSync(file, 'w');
    const enc = new DeltaEncoder();
    try {
      for (const rec of this.buf) writeAll(fd, enc.accept(rec));
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
    this.runs.push(file);
    this.buf = [];
  }

  /** Validate the (possibly out-of-order) fed records and publish a version. */
  commit(): string {
    if (this.done) throw new Error('builder is already finished');
    this.done = true;
    this.buf.sort((a, b) => comparePaths(a.path, b.path));
    const sources: Array<Iterator<Rec>> = this.runs.map((f) => fileRecords(f));
    sources.push(this.buf[Symbol.iterator]());
    try {
      return this.store.commitSorted(mergeIterators(sources), this.id);
    } finally {
      for (const f of this.runs) {
        try {
          fs.unlinkSync(f);
        } catch {}
      }
    }
  }

  abort(): void {
    this.done = true;
    for (const f of this.runs) {
      try {
        fs.unlinkSync(f);
      } catch {}
    }
    this.runs = [];
  }
}
