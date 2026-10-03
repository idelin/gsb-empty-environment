/**
 * Lock-free MVCC read path over SharedArrayBuffer.
 *
 * Design (single-writer / multi-reader, read-only transactions never block):
 *
 * - Every key owns an immutable, prepend-only version chain. A version node is
 *   written once (txId, key, value, next) and never mutated afterwards.
 * - The writer stages a multi-key transaction by appending one node per key
 *   and publishing each node with `Atomics.store(heads, key, idx)`
 *   (sequentially-consistent => release semantics). Only after ALL nodes are
 *   published does it bump the global `commitCounter` (another seq_cst store).
 * - A read-only transaction takes a snapshot by loading `commitCounter`
 *   (seq_cst => acquire), then walks each chain skipping nodes whose
 *   `txId > snapshot`. Those nodes belong to transactions that had not
 *   committed when the snapshot was taken, so the reader transparently falls
 *   through to the newest committed version.
 *
 * Why there is zero tearing without any lock or retry:
 *   seq_cst gives a single total order over all Atomics operations.
 *   writer:  publish(node_i) < bump(counter = T)
 *   reader:  load(counter)   < load(head_i)
 *   If the reader's snapshot >= T, then load(head_i) is ordered after
 *   bump(counter), hence after publish(node_i): the reader sees the new node.
 *   If the snapshot < T, the reader may see the new node but skips it by txId
 *   and lands on the older committed version. Either way every key is read as
 *   of the same logical commit point, so multi-key invariants (e.g. a
 *   conserved checksum) always hold.
 *
 * Neither readers nor the writer ever call `Atomics.wait`: the read path is
 * wait-free and the write path never contends with readers.
 */

export const NIL = -1;

// Control block layout (Int32 slots).
export const CTRL_COMMIT_COUNTER = 0;
export const CTRL_NEXT_NODE = 1;
export const CTRL_DONE = 2;
export const CTRL_TX_COMMITTED_LO = 3; // stats: committed tx count (u32 pair)
export const CTRL_TX_COMMITTED_HI = 4;
export const CTRL_SIZE = 16;

export interface StoreLayout {
  keyCount: number;
  poolSize: number;
}

export interface StoreViews extends StoreLayout {
  sab: SharedArrayBuffer;
  control: Int32Array;
  heads: Int32Array;
  baseVal: Float64Array;
  nodeTx: Int32Array;
  nodeKey: Int32Array;
  nodeNext: Int32Array;
  nodeVal: Float64Array;
}

export function createStore(keyCount: number, poolSize: number): StoreViews {
  // 8-byte-aligned region first (Float64), then Int32 regions.
  const f64Count = keyCount + poolSize;
  const i32Count = CTRL_SIZE + keyCount + poolSize * 3;
  const sab = new SharedArrayBuffer(f64Count * 8 + i32Count * 4);

  const baseVal = new Float64Array(sab, 0, keyCount);
  const nodeVal = new Float64Array(sab, keyCount * 8, poolSize);

  const i32Base = f64Count * 8;
  let off = i32Base;
  const control = new Int32Array(sab, off, CTRL_SIZE);
  off += CTRL_SIZE * 4;
  const heads = new Int32Array(sab, off, keyCount);
  off += keyCount * 4;
  const nodeTx = new Int32Array(sab, off, poolSize);
  off += poolSize * 4;
  const nodeKey = new Int32Array(sab, off, poolSize);
  off += poolSize * 4;
  const nodeNext = new Int32Array(sab, off, poolSize);

  heads.fill(NIL);
  nodeNext.fill(NIL);

  return { sab, keyCount, poolSize, control, heads, baseVal, nodeTx, nodeKey, nodeNext, nodeVal };
}

export function attachStore(sab: SharedArrayBuffer, keyCount: number, poolSize: number): StoreViews {
  const baseVal = new Float64Array(sab, 0, keyCount);
  const nodeVal = new Float64Array(sab, keyCount * 8, poolSize);
  const i32Base = (keyCount + poolSize) * 8;
  let off = i32Base;
  const control = new Int32Array(sab, off, CTRL_SIZE);
  off += CTRL_SIZE * 4;
  const heads = new Int32Array(sab, off, keyCount);
  off += keyCount * 4;
  const nodeTx = new Int32Array(sab, off, poolSize);
  off += poolSize * 4;
  const nodeKey = new Int32Array(sab, off, poolSize);
  off += poolSize * 4;
  const nodeNext = new Int32Array(sab, off, poolSize);
  const nodeValRef = nodeVal;
  return { sab, keyCount, poolSize, control, heads, baseVal, nodeTx, nodeKey, nodeNext, nodeVal: nodeValRef };
}

/**
 * The single writer. All methods are plain (non-atomic) stores except the
 * final release-publish of each head pointer and the commit counter bump.
 * The writer never waits on readers.
 */
export class MvccWriter {
  private store: StoreViews;
  private pendingTxId = 0;
  private inTxn = false;

  constructor(store: StoreViews) {
    this.store = store;
  }

  begin(): void {
    if (this.inTxn) throw new Error("transaction already in progress");
    this.pendingTxId = Atomics.load(this.store.control, CTRL_COMMIT_COUNTER) + 1;
    this.inTxn = true;
  }

  /** Stage a write: build an immutable node and release-publish it. */
  put(key: number, value: number): void {
    if (!this.inTxn) throw new Error("put() outside of transaction");
    const s = this.store;
    const idx = s.control[CTRL_NEXT_NODE];
    if (idx >= s.poolSize) throw new Error("version pool exhausted");
    s.control[CTRL_NEXT_NODE] = idx + 1;
    // Node fields are fully initialized BEFORE the head pointer is published.
    s.nodeTx[idx] = this.pendingTxId;
    s.nodeKey[idx] = key;
    s.nodeVal[idx] = value;
    s.nodeNext[idx] = Atomics.load(s.heads, key);
    Atomics.store(s.heads, key, idx); // release publish
  }

  /** Commit: one seq_cst store makes the whole batch visible atomically. */
  commit(): number {
    if (!this.inTxn) throw new Error("commit() outside of transaction");
    Atomics.store(this.store.control, CTRL_COMMIT_COUNTER, this.pendingTxId);
    this.inTxn = false;
    return this.pendingTxId;
  }
}

/**
 * A read-only transaction snapshot. Lock-free: no locks, no CAS loops, no
 * retries, no Atomics.wait. A snapshot is a single atomic load; every read
 * then traverses immutable nodes only.
 */
export class MvccReader {
  private store: StoreViews;

  constructor(store: StoreViews) {
    this.store = store;
  }

  /** Take a snapshot: one acquire load of the global commit counter. */
  snapshot(): number {
    return Atomics.load(this.store.control, CTRL_COMMIT_COUNTER);
  }

  /** Read one key as of `snap`. Never blocks, never retries. */
  get(snap: number, key: number): number {
    const s = this.store;
    let idx = Atomics.load(s.heads, key); // acquire
    while (idx !== NIL && s.nodeTx[idx] > snap) {
      idx = s.nodeNext[idx]; // immutable after publication
    }
    return idx === NIL ? s.baseVal[key] : s.nodeVal[idx];
  }

  /** Full-table scan as of `snap`; returns the checksum (sum of all keys). */
  checksum(snap: number): number {
    let sum = 0;
    for (let key = 0; key < this.store.keyCount; key++) {
      sum += this.get(snap, key);
    }
    return sum;
  }
}

/** Convenience helper used by the single-threaded tests. */
export function commitBatch(store: StoreViews, writer: MvccWriter, writes: Array<[number, number]>): number {
  writer.begin();
  for (const [key, value] of writes) writer.put(key, value);
  return writer.commit();
}
