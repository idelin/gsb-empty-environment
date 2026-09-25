/**
 * Streaming path-record wire format.
 *
 * A tree is described as a stream of records, one per node. Each record:
 *
 *   record := varint(pop) varint(nseg) seg* op value?
 *   seg    := 0x00 varint(len) utf8        -- object key (keys stay strings,
 *   |                                       so {"0":"x"} is never an array)
 *            0x01 varint(index)            -- array index
 *   op     := 0 Null | 1 Missing | 2 Hole | 3 False | 4 True
 *   |         5 Num(f64 LE) | 6 Str(varint len + utf8) | 7 Obj | 8 Arr
 *   value  := present only for Num / Str
 *
 * Paths are delta-encoded against the previous record's path: `pop` segments
 * are dropped, then `nseg` new segments appended. A depth-D chain therefore
 * costs O(D) bytes total, and the decoder never copies path arrays.
 *
 * Semantics of the ops:
 *   Null     explicit JSON null (distinct from Missing)
 *   Missing  tombstone: "this path is absent" -- only legal inside patches
 *   Hole     array elision ([1,,3]); the slot stays empty, length preserved
 *   Obj/Arr  container declaration; an empty container is simply a container
 *            record with no child records, so {} and [] survive a round-trip
 */
import { Buffer } from 'node:buffer';
import { FormatError } from './errors.ts';
import { formatPath, fromArray, pushSeg, segEqual, type Path, type PNode, type Seg } from './path.ts';

export const Op = {
  Null: 0,
  Missing: 1,
  Hole: 2,
  False: 3,
  True: 4,
  Num: 5,
  Str: 6,
  Obj: 7,
  Arr: 8,
} as const;
export type OpCode = (typeof Op)[keyof typeof Op];

export interface Rec {
  path: Path;
  op: OpCode;
  val?: string | number;
}

/** Delta-encoded record as it appears on the wire. */
export interface RawRec {
  pop: number;
  segs: Seg[];
  op: OpCode;
  val?: string | number;
}

export function isContainerOp(op: OpCode): boolean {
  return op === Op.Obj || op === Op.Arr;
}

export function canHaveChildren(op: OpCode): boolean {
  return isContainerOp(op);
}

// ---------------------------------------------------------------- varints --

export function encodeVarint(n: number): Buffer {
  if (!Number.isSafeInteger(n) || n < 0) throw new FormatError(`varint out of range: ${n}`);
  const bytes: number[] = [];
  do {
    let b = n % 128;
    n = Math.floor(n / 128);
    if (n > 0) b |= 0x80;
    bytes.push(b);
  } while (n > 0);
  return Buffer.from(bytes);
}

function readVarint(buf: Buffer, off: number): { value: number; next: number } | null {
  let value = 0;
  let shift = 0;
  let p = off;
  for (;;) {
    if (p >= buf.length) return null; // incomplete, wait for more bytes
    const b = buf[p++];
    value += (b & 0x7f) * 2 ** shift;
    if ((b & 0x80) === 0) break;
    shift += 7;
    if (shift > 49) throw new FormatError(`varint too long at offset ${off}`);
  }
  return { value, next: p };
}

// ---------------------------------------------------------------- encoder --

export function encodeRawRec(raw: RawRec): Buffer {
  const parts: Buffer[] = [encodeVarint(raw.pop), encodeVarint(raw.segs.length)];
  for (const seg of raw.segs) {
    if (seg.t === 'k') {
      const kb = Buffer.from(seg.k, 'utf8');
      parts.push(Buffer.from([0]), encodeVarint(kb.length), kb);
    } else {
      parts.push(Buffer.from([1]), encodeVarint(seg.i));
    }
  }
  parts.push(Buffer.from([raw.op]));
  if (raw.op === Op.Str) {
    if (typeof raw.val !== 'string') throw new FormatError('Str record without string value');
    const vb = Buffer.from(raw.val, 'utf8');
    parts.push(encodeVarint(vb.length), vb);
  } else if (raw.op === Op.Num) {
    if (typeof raw.val !== 'number') throw new FormatError('Num record without number value');
    const nb = Buffer.allocUnsafe(8);
    nb.writeDoubleLE(raw.val, 0);
    parts.push(nb);
  }
  return Buffer.concat(parts);
}

/**
 * Incremental encoder: accepts absolute-path records in any order and emits
 * delta-encoded bytes. Keeps only the previous path.
 */
export class DeltaEncoder {
  private cur: Path = null;

  accept(rec: Rec): Buffer {
    let a = this.cur;
    let b = rec.path;
    let pops = 0;
    const suffix: Seg[] = [];
    let da = a ? a.depth : 0;
    let db = b ? b.depth : 0;
    while (da > db) {
      a = a!.up;
      pops++;
      da--;
    }
    while (db > da) {
      suffix.push(b!.seg);
      b = b!.up;
      db--;
    }
    while (a && b && a !== b && !segEqual(a.seg, b.seg)) {
      suffix.push(b.seg);
      a = a.up;
      b = b.up;
      pops++;
    }
    suffix.reverse();
    this.cur = rec.path;
    return encodeRawRec({ pop: pops, segs: suffix, op: rec.op, val: rec.val });
  }
}

// ---------------------------------------------------------------- decoder --

function tryParseRaw(buf: Buffer, off: number): { raw: RawRec; next: number } | null {
  let p = off;
  const pop = readVarint(buf, p);
  if (!pop) return null;
  p = pop.next;
  const cnt = readVarint(buf, p);
  if (!cnt) return null;
  p = cnt.next;
  const segs: Seg[] = [];
  for (let i = 0; i < cnt.value; i++) {
    if (p >= buf.length) return null;
    const tag = buf[p++];
    if (tag === 0) {
      const len = readVarint(buf, p);
      if (!len) return null;
      p = len.next;
      if (p + len.value > buf.length) return null;
      segs.push({ t: 'k', k: buf.toString('utf8', p, p + len.value) });
      p += len.value;
    } else if (tag === 1) {
      const idx = readVarint(buf, p);
      if (!idx) return null;
      p = idx.next;
      segs.push({ t: 'i', i: idx.value });
    } else {
      throw new FormatError(`bad segment tag ${tag} at offset ${p - 1}`);
    }
  }
  if (p >= buf.length) return null;
  const op = buf[p++] as OpCode;
  if (op < 0 || op > 8) throw new FormatError(`bad op ${op} at offset ${p - 1}`);
  let val: string | number | undefined;
  if (op === Op.Str) {
    const len = readVarint(buf, p);
    if (!len) return null;
    p = len.next;
    if (p + len.value > buf.length) return null;
    val = buf.toString('utf8', p, p + len.value);
    p += len.value;
  } else if (op === Op.Num) {
    if (p + 8 > buf.length) return null;
    val = buf.readDoubleLE(p);
    p += 8;
  }
  return { raw: { pop: pop.value, segs, op, val }, next: p };
}

/**
 * Incremental decoder. Feed arbitrary byte chunks; get back complete records
 * with absolute (cons-list) paths. Paths are interned, so equal paths share
 * structure and a deep chain costs O(1) memory per record.
 */
export class RecordDecoder {
  private buf: Buffer = Buffer.alloc(0);
  private cur: Path = null;
  private intern = new Map<string, PNode>();
  private nextId = 1;

  push(chunk: Buffer): Rec[] {
    this.buf = this.buf.length === 0 ? Buffer.from(chunk) : Buffer.concat([this.buf, chunk]);
    const out: Rec[] = [];
    let off = 0;
    while (off < this.buf.length) {
      const r = tryParseRaw(this.buf, off);
      if (!r) break;
      off = r.next;
      let p: Path = this.cur;
      for (let i = 0; i < r.raw.pop; i++) {
        if (!p) throw new FormatError('path delta pops above the root');
        p = p.up;
      }
      for (const seg of r.raw.segs) p = this.interned(p, seg);
      this.cur = p;
      out.push({ path: p, op: r.raw.op, val: r.raw.val });
    }
    this.buf = this.buf.subarray(off);
    return out;
  }

  /** Assert the stream ended on a record boundary. */
  finish(): void {
    if (this.buf.length !== 0) throw new FormatError('truncated record stream');
  }

  private interned(up: Path, seg: Seg): PNode {
    const key = `${up ? up.id : 0}|${seg.t}|${seg.t === 'k' ? seg.k : seg.i}`;
    let node = this.intern.get(key);
    if (!node) {
      node = { seg, up, depth: (up ? up.depth : 0) + 1, id: this.nextId++ };
      this.intern.set(key, node);
    }
    return node;
  }
}

// ------------------------------------------------------------- tree walk --

/**
 * Iterative DFS over a JSON-compatible value, yielding delta-encoded records.
 * Objects use Object.keys (key order is irrelevant; the store canonicalizes),
 * arrays are walked in index order with explicit Hole records for elisions.
 */
export function* encodeTree(root: unknown): Generator<RawRec> {
  interface Frame {
    value: unknown;
    keys: string[] | null; // non-null for objects
    len: number;
    next: number;
  }
  const stack: Frame[] = [];
  const path: Seg[] = []; // path of the innermost open container (for errors)
  let lastLen = 0; // path length of the previously emitted record

  const where = (): string => formatPath(fromArray(path));

  const enter = (value: unknown): { op: OpCode; val?: string | number; frame?: Frame } => {
    if (value === null) return { op: Op.Null };
    switch (typeof value) {
      case 'boolean':
        return { op: value ? Op.True : Op.False };
      case 'number':
        if (!Number.isFinite(value)) throw new FormatError(`non-finite number at ${where()}`);
        return { op: Op.Num, val: value };
      case 'string':
        return { op: Op.Str, val: value };
      case 'object':
        if (Array.isArray(value)) return { op: Op.Arr, frame: { value, keys: null, len: value.length, next: 0 } };
        const keys = Object.keys(value as Record<string, unknown>);
        return { op: Op.Obj, frame: { value, keys, len: keys.length, next: 0 } };
      default:
        throw new FormatError(`non-JSON value of type ${typeof value} at ${where()}`);
    }
  };

  const first = enter(root);
  yield { pop: 0, segs: [], op: first.op, val: first.val };
  if (!first.frame) return;
  stack.push(first.frame);

  while (stack.length > 0) {
    const f = stack[stack.length - 1];
    if (f.next >= f.len) {
      stack.pop();
      path.pop();
      continue;
    }
    let seg: Seg;
    let child: unknown;
    let hole = false;
    if (f.keys) {
      const k = f.keys[f.next++];
      seg = { t: 'k', k };
      child = (f.value as Record<string, unknown>)[k];
    } else {
      const i = f.next++;
      seg = { t: 'i', i };
      if (i in (f.value as unknown[])) child = (f.value as unknown[])[i];
      else hole = true;
    }
    const pop = lastLen - path.length;
    if (hole) {
      yield { pop, segs: [seg], op: Op.Hole };
      lastLen = path.length + 1;
      continue;
    }
    path.push(seg);
    const e = enter(child);
    path.pop();
    yield { pop, segs: [seg], op: e.op, val: e.val };
    lastLen = path.length + 1;
    if (e.frame) {
      path.push(seg);
      stack.push(e.frame);
    }
  }
}

export function encodeTreeBuffer(root: unknown): Buffer {
  const parts: Buffer[] = [];
  for (const raw of encodeTree(root)) parts.push(encodeRawRec(raw));
  return Buffer.concat(parts);
}

/** Convenience: decode(encode(tree)) as a record generator. */
export function* treeRecords(root: unknown): Generator<Rec> {
  const dec = new RecordDecoder();
  for (const raw of encodeTree(root)) yield* dec.push(encodeRawRec(raw));
  dec.finish();
}
