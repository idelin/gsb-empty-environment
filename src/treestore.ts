import * as fs from "node:fs/promises";
import * as path from "node:path";
import * as readline from "node:readline";
import { createReadStream } from "node:fs";
import { buildTree } from "./builder.ts";
import { PathError } from "./errors.ts";
import { lookupNode, nodeToRecords, type TreeNode } from "./node.ts";
import {
  PathInterner,
  comparePathNodes,
  formatPath,
  isPrefixNode,
  isProperPrefixNode,
  pathToArray,
  type PathNode,
  type Seg,
} from "./path.ts";
import {
  compareRecords,
  decodeRecord,
  encodeRecord,
  type PathRecord,
} from "./record.ts";
import type { SourcedRecord } from "./store.ts";

export interface Version {
  readonly id: number;
  readonly root: TreeNode;
  readonly count: number;
}

export type RecordSource =
  | Iterable<PathRecord>
  | AsyncIterable<PathRecord>
  | AsyncIterable<SourcedRecord>;

export interface CommitOptions {
  /** Failure-injection hook: persist the new index but never publish HEAD. */
  crashBeforePublish?: boolean;
}

export interface PatchResult {
  version: Version;
  /** Records that undo this change when passed to applyPatch. */
  inverse: PathRecord[];
}

async function* normalize(source: RecordSource): AsyncGenerator<SourcedRecord> {
  let n = 0;
  for await (const item of source as AsyncIterable<PathRecord | SourcedRecord>) {
    n++;
    if (typeof item === "object" && item !== null && "rec" in item) {
      yield item as SourcedRecord;
    } else {
      yield { rec: item as PathRecord, origin: `input#${n}` };
    }
  }
}

/** A read handle pinned to a specific version; versions are immutable. */
export class Cursor {
  readonly version: Version;
  readonly prefix: PathNode;
  private readonly interner: PathInterner;

  constructor(version: Version, prefix: PathNode, interner: PathInterner) {
    this.version = version;
    this.prefix = prefix;
    this.interner = interner;
  }

  node(): TreeNode | undefined {
    return lookupNode(this.version.root, this.prefix);
  }

  *records(): Generator<PathRecord> {
    const found = this.node();
    if (found !== undefined) yield* nodeToRecords(found, this.interner, this.prefix);
  }
}

/**
 * Versioned tree store. A commit validates the incoming record stream first
 * and only then publishes: in memory by swapping the head reference, on disk
 * by writing `index-<id>.jsonl` + fsync and then atomically renaming
 * `HEAD.tmp` to `HEAD`. A crash between the two steps leaves an orphan index
 * that recovery removes without ever exposing the half-written version.
 */
export class TreeStore {
  readonly interner = new PathInterner();
  private head: Version | undefined;
  private readonly dir: string | null;

  private constructor(dir: string | null) {
    this.dir = dir;
  }

  static async open(opts: { dir?: string } = {}): Promise<TreeStore> {
    const store = new TreeStore(opts.dir ?? null);
    if (opts.dir !== undefined) {
      await fs.mkdir(opts.dir, { recursive: true });
      await store.recover();
    }
    return store;
  }

  get current(): Version | undefined {
    return this.head;
  }

  private requireHead(): Version {
    if (this.head === undefined) throw new PathError("NoVersion", [], "no committed version yet");
    return this.head;
  }

  private async recover(): Promise<void> {
    const dir = this.dir as string;
    let headId: number | undefined;
    try {
      headId = Number(await fs.readFile(path.join(dir, "HEAD"), "utf8"));
    } catch {
      headId = undefined;
    }
    for (const file of await fs.readdir(dir)) {
      const m = /^index-(\d+)\.jsonl$/.exec(file);
      if (m !== null && Number(m[1]) !== headId) {
        await fs.rm(path.join(dir, file), { force: true });
      }
    }
    if (headId === undefined) return;
    const indexFile = path.join(dir, `index-${headId}.jsonl`);
    const store = this;
    async function* readIndex(): AsyncGenerator<SourcedRecord> {
      const rl = readline.createInterface({
        input: createReadStream(indexFile),
        crlfDelay: Number.POSITIVE_INFINITY,
      });
      let lineNo = 0;
      for await (const line of rl) {
        lineNo++;
        if (line.length === 0) continue;
        const origin = `${indexFile}:${lineNo}`;
        yield { rec: decodeRecord(line, store.interner, origin), origin };
      }
    }
    const { root, count } = await buildTree(readIndex());
    this.head = { id: headId, root, count };
  }

  async commit(source: RecordSource, opts: CommitOptions = {}): Promise<Version> {
    // Validate everything before anything is published or persisted.
    const { root, count } = await buildTree(normalize(source));
    const id = (this.head?.id ?? 0) + 1;
    const version: Version = { id, root, count };
    if (this.dir !== null) {
      const indexFile = path.join(this.dir, `index-${id}.jsonl`);
      await this.writeIndex(indexFile, root);
      if (opts.crashBeforePublish === true) return version; // simulated crash
      await this.publishHead(id);
      if (this.head !== undefined) {
        await fs.rm(path.join(this.dir, `index-${this.head.id}.jsonl`), { force: true });
      }
    }
    this.head = version; // atomic in-memory publication
    return version;
  }

  private async writeIndex(indexFile: string, root: TreeNode): Promise<void> {
    const fh = await fs.open(indexFile, "w");
    try {
      for (const rec of nodeToRecords(root, this.interner, this.interner.root)) {
        await fh.write(encodeRecord(rec) + "\n");
      }
      await fh.sync();
    } finally {
      await fh.close();
    }
  }

  private async publishHead(id: number): Promise<void> {
    const dir = this.dir as string;
    const tmp = path.join(dir, "HEAD.tmp");
    const fh = await fs.open(tmp, "w");
    try {
      await fh.write(String(id));
      await fh.sync();
    } finally {
      await fh.close();
    }
    await fs.rename(tmp, path.join(dir, "HEAD"));
  }

  /** Prefix query: a cursor pinned to `version` (default: current head). */
  cursor(prefix: Seg[] | PathNode, version?: Version): Cursor {
    const v = version ?? this.requireHead();
    const node = Array.isArray(prefix) ? this.interner.internPath(prefix) : prefix;
    return new Cursor(v, node, this.interner);
  }

  /** Sorted record stream of a version (default: current head). */
  async *records(version?: Version): AsyncGenerator<SourcedRecord> {
    const v = version ?? this.requireHead();
    let n = 0;
    for (const rec of nodeToRecords(v.root, this.interner, this.interner.root)) {
      n++;
      yield { rec, origin: `version-${v.id}#${n}` };
    }
  }

  /**
   * Applies a patch (records + tombstones) to the current head and commits
   * the result. Patch records replace whole subtrees; `del` removes them.
   * Returns the new version and the inverse patch.
   */
  async applyPatch(patch: PathRecord[]): Promise<PatchResult> {
    this.requireHead();
    const sorted = [...patch].sort(compareRecords);
    const inverse: PathRecord[] = [];
    const version = await this.commit(this.mergePatch(sorted, inverse));
    return { version, inverse };
  }

  private async *mergePatch(
    patch: PathRecord[],
    inverse: PathRecord[],
  ): AsyncGenerator<SourcedRecord> {
    const current = this.records();
    let cur = await current.next();
    let pi = 0;
    let topPatch: PathNode | null = null;

    const skipCurrentSubtree = async (at: PathNode, collect: PathRecord[] | null): Promise<void> => {
      while (cur.done !== true && isPrefixNode(at, cur.value.rec.path)) {
        if (collect !== null) collect.push(cur.value.rec);
        cur = await current.next();
      }
    };
    const isTopLevel = (at: PathNode): boolean =>
      topPatch === null || !isPrefixNode(topPatch, at);

    while ((cur.done !== true) || pi < patch.length) {
      if (pi >= patch.length) {
        yield cur.value as SourcedRecord;
        cur = await current.next();
        continue;
      }
      const p = patch[pi];
      if (cur.done === true || comparePathNodes(p.path, cur.value.rec.path) < 0) {
        // Nothing in the current tree at this path: pure addition.
        if (isTopLevel(p.path)) {
          topPatch = p.path;
          if (p.op !== "del") inverse.push({ path: p.path, op: "del" });
        }
        if (p.op !== "del") yield { rec: p, origin: "patch" };
        pi++;
        continue;
      }
      const cmp = comparePathNodes(p.path, cur.value.rec.path);
      if (cmp > 0) {
        yield cur.value as SourcedRecord;
        cur = await current.next();
        continue;
      }
      // Same path: delete, header-update, or replace the current subtree.
      const top = isTopLevel(p.path);
      if (top) topPatch = p.path;
      const curRec = cur.value.rec as PathRecord;
      if (p.op === "del") {
        await skipCurrentSubtree(p.path, top ? inverse : null);
        pi++;
        continue;
      }
      // A container record replaced by the same kind of container only
      // updates the header (e.g. array length); its children are kept.
      const headerUpdate =
        (p.op === "arr" && curRec.op === "arr") || (p.op === "obj" && curRec.op === "obj");
      if (headerUpdate) {
        if (top) inverse.push(curRec);
        cur = await current.next();
      } else {
        await skipCurrentSubtree(p.path, top ? inverse : null);
      }
      yield { rec: p, origin: "patch" };
      pi++;
    }
  }

  /**
   * Moves the subtree at `src` to `dst` as an incremental diff against the
   * current head. Any validation failure leaves the head untouched.
   */
  async move(src: Seg[] | PathNode, dst: Seg[] | PathNode): Promise<PatchResult> {
    const head = this.requireHead();
    const srcNode = Array.isArray(src) ? this.interner.internPath(src) : src;
    const dstNode = Array.isArray(dst) ? this.interner.internPath(dst) : dst;
    const srcPath = pathToArray(srcNode);
    const dstPath = pathToArray(dstNode);
    const bad = (
      kind: ConstructorParameters<typeof PathError>[0],
      at: Seg[],
      msg: string,
    ): never => {
      throw new PathError(kind, at, `${msg} at ${formatPath(at)}`);
    };

    if (srcNode.depth === 0) bad("MoveIntoSelf", srcPath, "cannot move the root");
    const srcTree = lookupNode(head.root, srcNode);
    if (srcTree === undefined) bad("NotFound", srcPath, "move source does not exist");
    if (srcNode === dstNode || isProperPrefixNode(srcNode, dstNode)) {
      bad("MoveIntoSelf", dstPath, "cannot move a subtree into itself");
    }
    if (lookupNode(head.root, dstNode) !== undefined) {
      bad("TargetExists", dstPath, "move target already exists");
    }
    const dstParent = dstNode.parent as PathNode;
    const dstParentTree = lookupNode(head.root, dstParent);
    if (dstParentTree === undefined) {
      bad("NotFound", pathToArray(dstParent), "move target parent does not exist");
    }
    const dstSeg = dstNode.seg as Seg;
    const patch: PathRecord[] = [];
    let bumped: { path: PathNode; n: number } | undefined;
    if (dstParentTree.kind === "val") {
      bad("ParentTypeConflict", dstPath, "move target parent is a scalar");
    } else if (dstParentTree.kind === "obj") {
      if (dstSeg.t !== "k") {
        bad("SegmentTypeMismatch", dstPath, "object parent requires a string key segment");
      }
    } else {
      if (dstSeg.t !== "i") {
        bad("SegmentTypeMismatch", dstPath, "array parent requires an index segment");
      }
      if (dstSeg.i > dstParentTree.slots.length) {
        bad("IndexOutOfRange", dstPath, `index ${dstSeg.i} exceeds array length`);
      }
      if (dstSeg.i === dstParentTree.slots.length) {
        bumped = { path: dstParent, n: dstParentTree.slots.length };
        patch.push({ path: dstParent, op: "arr", n: dstSeg.i + 1 });
      }
    }

    for (const rec of nodeToRecords(srcTree, this.interner, dstNode)) patch.push(rec);
    patch.push({ path: srcNode, op: "del" });
    const { version } = await this.applyPatch(patch);

    const inverse: PathRecord[] = [{ path: dstNode, op: "del" }];
    for (const rec of nodeToRecords(srcTree, this.interner, srcNode)) inverse.push(rec);
    if (bumped !== undefined) {
      inverse.push({ path: bumped.path, op: "arr", n: bumped.n });
    }
    return { version, inverse };
  }
}
