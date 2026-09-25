/**
 * 差量历史核心：
 * - 局部像素编辑只存变化像素的 span 差量（前态/后态），RLE 压缩；
 * - 元数据（尺寸）变化作为结构事务，存整图前态/后态快照（RLE 压缩）；
 * - 历史组织为树：撤销后再编辑产生分支，可选择分支重做；
 * - 超过容量时淘汰：优先剪除不在当前路径上的死分支，
 *   再把最老事务前滚合并进基线快照——淘汰只动历史末端，不影响当前图。
 */
import { RGBAImage } from './image.ts';
import { rleEncode, rleDecode } from './rle.ts';

export interface ImageMeta {
  width: number;
  height: number;
}

/** 连续变化像素段（以像素为单位）。 */
export interface PixelSpan {
  offset: number; // 起始像素下标
  length: number; // 像素个数
}

export class Transaction {
  readonly id: number;
  readonly label: string;
  readonly metaBefore: ImageMeta;
  readonly metaAfter: ImageMeta;
  /** true 表示尺寸变化等结构事务，负载为整图数据；false 表示局部 span 差量 */
  readonly structural: boolean;
  readonly spans: PixelSpan[];
  /** RLE 压缩的前态字节（结构事务为整图，否则为各 span 拼接） */
  readonly beforePacked: Uint8Array;
  readonly afterPacked: Uint8Array;

  constructor(
    id: number,
    label: string,
    metaBefore: ImageMeta,
    metaAfter: ImageMeta,
    structural: boolean,
    spans: PixelSpan[],
    beforePacked: Uint8Array,
    afterPacked: Uint8Array,
  ) {
    this.id = id;
    this.label = label;
    this.metaBefore = metaBefore;
    this.metaAfter = metaAfter;
    this.structural = structural;
    this.spans = spans;
    this.beforePacked = beforePacked;
    this.afterPacked = afterPacked;
  }

  get payloadBytes(): number {
    return this.beforePacked.length + this.afterPacked.length;
  }
}

/** 由前后两图 diff 出 span 差量事务（局部像素编辑）。 */
export function diffTransaction(
  id: number,
  label: string,
  before: RGBAImage,
  after: RGBAImage,
): Transaction {
  if (before.width !== after.width || before.height !== after.height) {
    throw new Error('diffTransaction requires equal dimensions; use snapshotTransaction');
  }
  const spans: PixelSpan[] = [];
  const beforeBytes: number[] = [];
  const afterBytes: number[] = [];
  const pixelCount = before.width * before.height;
  let i = 0;
  while (i < pixelCount) {
    const o = i * 4;
    const same =
      before.data[o] === after.data[o] &&
      before.data[o + 1] === after.data[o + 1] &&
      before.data[o + 2] === after.data[o + 2] &&
      before.data[o + 3] === after.data[o + 3];
    if (same) {
      i++;
      continue;
    }
    const start = i;
    while (i < pixelCount) {
      const p = i * 4;
      const eq =
        before.data[p] === after.data[p] &&
        before.data[p + 1] === after.data[p + 1] &&
        before.data[p + 2] === after.data[p + 2] &&
        before.data[p + 3] === after.data[p + 3];
      if (eq) break;
      i++;
    }
    const length = i - start;
    spans.push({ offset: start, length });
    for (let k = start * 4; k < (start + length) * 4; k++) {
      beforeBytes.push(before.data[k]);
      afterBytes.push(after.data[k]);
    }
  }
  return new Transaction(
    id,
    label,
    { width: before.width, height: before.height },
    { width: after.width, height: after.height },
    false,
    spans,
    rleEncode(Uint8Array.from(beforeBytes), 4),
    rleEncode(Uint8Array.from(afterBytes), 4),
  );
}

/** 结构事务（裁剪/尺寸变化）：整图前后态快照。 */
export function snapshotTransaction(
  id: number,
  label: string,
  before: RGBAImage,
  after: RGBAImage,
): Transaction {
  return new Transaction(
    id,
    label,
    { width: before.width, height: before.height },
    { width: after.width, height: after.height },
    true,
    [],
    rleEncode(before.data, 4),
    rleEncode(after.data, 4),
  );
}

/** 应用事务（redo 方向），返回新图。 */
export function applyTransaction(image: RGBAImage, tx: Transaction): RGBAImage {
  if (tx.structural) {
    return new RGBAImage(
      tx.metaAfter.width,
      tx.metaAfter.height,
      rleDecode(tx.afterPacked, 4, tx.metaAfter.width * tx.metaAfter.height * 4),
    );
  }
  const out = image.clone();
  const decoded = rleDecode(tx.afterPacked, 4);
  let cursor = 0;
  for (const span of tx.spans) {
    out.data.set(decoded.subarray(cursor, cursor + span.length * 4), span.offset * 4);
    cursor += span.length * 4;
  }
  return out;
}

/** 逆放事务（undo 方向），返回新图。重叠编辑逆序恢复依赖此处只写回本事务的前态。 */
export function revertTransaction(image: RGBAImage, tx: Transaction): RGBAImage {
  if (tx.structural) {
    return new RGBAImage(
      tx.metaBefore.width,
      tx.metaBefore.height,
      rleDecode(tx.beforePacked, 4, tx.metaBefore.width * tx.metaBefore.height * 4),
    );
  }
  const out = image.clone();
  const decoded = rleDecode(tx.beforePacked, 4);
  let cursor = 0;
  for (const span of tx.spans) {
    out.data.set(decoded.subarray(cursor, cursor + span.length * 4), span.offset * 4);
    cursor += span.length * 4;
  }
  return out;
}

interface HistoryNode {
  id: number;
  parent: number; // 0 表示基线
  tx: Transaction;
  children: number[];
  seq: number; // 提交序号，用于分支新旧排序与淘汰
}

export interface BranchInfo {
  transactionId: number;
  label: string;
  isCurrentPath: boolean;
}

export interface HistoryStats {
  liveTransactions: number;
  evictedIntoBase: number;
  prunedBranchTransactions: number;
  payloadBytes: number;
  baseBytes: number;
  undoDepth: number;
}

const BASE_ID = 0;

export class History {
  private base: { meta: ImageMeta; dataPacked: Uint8Array };
  private nodes = new Map<number, HistoryNode>();
  private childrenOfBase: number[] = [];
  private headId: number = BASE_ID;
  private nextTxId = 1;
  private nextSeq = 1;
  private evictedIntoBase = 0;
  private prunedBranchTransactions = 0;
  readonly maxTransactions: number;

  constructor(initial: RGBAImage, maxTransactions: number = 128) {
    this.maxTransactions = maxTransactions;
    this.base = {
      meta: { width: initial.width, height: initial.height },
      dataPacked: rleEncode(initial.data, 4),
    };
  }

  get headTransactionId(): number | null {
    return this.headId === BASE_ID ? null : this.headId;
  }

  /** 记录一个已应用到图上的事务；若当前不在分支末端则产生新分支。 */
  commit(tx: Transaction): void {
    const node: HistoryNode = {
      id: tx.id,
      parent: this.headId,
      tx,
      children: [],
      seq: this.nextSeq++,
    };
    this.nodes.set(tx.id, node);
    if (this.headId === BASE_ID) {
      this.childrenOfBase.push(tx.id);
    } else {
      this.nodes.get(this.headId)!.children.push(tx.id);
    }
    this.headId = tx.id;
    this.enforceLimit();
  }

  /** 撤销一步，返回需要逆放的事务；已在基线则返回 null。 */
  undo(): Transaction | null {
    if (this.headId === BASE_ID) return null;
    const node = this.nodes.get(this.headId)!;
    this.headId = node.parent;
    return node.tx;
  }

  /**
   * 重做一步。head 有多个子节点时可传入 transactionId 选择分支，
   * 默认走最新（序号最大）的分支。
   */
  redo(transactionId?: number): Transaction | null {
    const children = this.childrenOf(this.headId);
    if (children.length === 0) return null;
    let next: HistoryNode | undefined;
    if (transactionId !== undefined) {
      next = children.find((c) => c.id === transactionId);
      if (!next) throw new Error(`transaction ${transactionId} is not a child of current head`);
    } else {
      next = children.reduce((a, b) => (a.seq >= b.seq ? a : b));
    }
    this.headId = next.id;
    return next.tx;
  }

  /** 当前位置可重做的分支列表。 */
  branches(): BranchInfo[] {
    return this.childrenOf(this.headId)
      .map((c) => ({ transactionId: c.id, label: c.tx.label, isCurrentPath: false }))
      .sort((a, b) => a.transactionId - b.transactionId);
  }

  allocateTransactionId(): number {
    return this.nextTxId++;
  }

  stats(): HistoryStats {
    let payloadBytes = 0;
    for (const node of this.nodes.values()) payloadBytes += node.tx.payloadBytes;
    return {
      liveTransactions: this.nodes.size,
      evictedIntoBase: this.evictedIntoBase,
      prunedBranchTransactions: this.prunedBranchTransactions,
      payloadBytes,
      baseBytes: this.base.dataPacked.length,
      undoDepth: this.depthOf(this.headId),
    };
  }

  /** 由基线重放到 head，校验/重建当前图（测试与调试用）。 */
  replayToHead(): RGBAImage {
    let image = new RGBAImage(
      this.base.meta.width,
      this.base.meta.height,
      rleDecode(this.base.dataPacked, 4, this.base.meta.width * this.base.meta.height * 4),
    );
    for (const id of this.pathToHead()) {
      image = applyTransaction(image, this.nodes.get(id)!.tx);
    }
    return image;
  }

  private childrenOf(id: number): HistoryNode[] {
    const ids = id === BASE_ID ? this.childrenOfBase : this.nodes.get(id)!.children;
    return ids.map((cid) => this.nodes.get(cid)!);
  }

  private depthOf(id: number): number {
    let depth = 0;
    let cur = id;
    while (cur !== BASE_ID) {
      cur = this.nodes.get(cur)!.parent;
      depth++;
    }
    return depth;
  }

  private pathToHead(): number[] {
    const path: number[] = [];
    let cur = this.headId;
    while (cur !== BASE_ID) {
      path.push(cur);
      cur = this.nodes.get(cur)!.parent;
    }
    return path.reverse();
  }

  private isOnHeadPath(id: number): boolean {
    let cur = this.headId;
    while (cur !== BASE_ID) {
      if (cur === id) return true;
      cur = this.nodes.get(cur)!.parent;
    }
    return false;
  }

  /** 容量控制：先剪死分支，再把最老事务前滚进基线。 */
  private enforceLimit(): void {
    // 1) 剪除不在当前路径上的死分支（最旧优先）
    for (;;) {
      if (this.nodes.size <= this.maxTransactions) return;
      const roots = this.childrenOfBase
        .map((id) => this.nodes.get(id)!)
        .filter((n) => !this.isOnHeadPath(n.id))
        .sort((a, b) => a.seq - b.seq);
      if (roots.length === 0) break;
      this.pruneSubtree(roots[0].id);
    }
    // 2) 基线唯一孩子可安全前滚合并
    for (;;) {
      if (this.nodes.size <= this.maxTransactions) return;
      if (this.childrenOfBase.length !== 1) {
        // 基线处有多个活分支，无法继续合并；保留现状（当前图始终不受影响）
        return;
      }
      const oldest = this.nodes.get(this.childrenOfBase[0])!;
      this.foldIntoBase(oldest);
    }
  }

  private pruneSubtree(rootId: number): void {
    const stack = [rootId];
    while (stack.length > 0) {
      const id = stack.pop()!;
      const node = this.nodes.get(id)!;
      stack.push(...node.children);
      this.nodes.delete(id);
      this.prunedBranchTransactions++;
    }
    this.childrenOfBase = this.childrenOfBase.filter((id) => id !== rootId);
    for (const node of this.nodes.values()) {
      node.children = node.children.filter((id) => id !== rootId);
    }
  }

  private foldIntoBase(node: HistoryNode): void {
    const baseImage = new RGBAImage(
      this.base.meta.width,
      this.base.meta.height,
      rleDecode(this.base.dataPacked, 4, this.base.meta.width * this.base.meta.height * 4),
    );
    const merged = applyTransaction(baseImage, node.tx);
    this.base = {
      meta: { width: merged.width, height: merged.height },
      dataPacked: rleEncode(merged.data, 4),
    };
    this.childrenOfBase = node.children;
    for (const childId of node.children) {
      this.nodes.get(childId)!.parent = BASE_ID;
    }
    this.nodes.delete(node.id);
    this.evictedIntoBase++;
  }
}
