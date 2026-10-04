// Append-only persistent log segment backed by a single file.
//
// On open the whole segment is read into one contiguous buffer (mmap-style
// image) and scanned once; every later lookup returns a zero-copy view into
// that image or into the per-append arena buffers. All mutating operations
// are serialized through an internal promise chain so concurrent callers
// observe a strict total order.

import { closeSync, existsSync, fstatSync, fsyncSync, ftruncateSync, openSync, readFileSync, writeSync } from 'node:fs';
import { Buffer } from 'node:buffer';
import { decodeEntryView, encodeEntry, scanFrames, type EntryInput, type EntryView } from './codec.ts';

interface Slot {
  arena: Buffer;
  offset: number;
  frameLen: number;
  fileOffset: number;
  term: bigint;
}

export class LogStore {
  readonly path: string;
  private fd: number;
  private readonly fsync: boolean;
  private readonly arenas: Buffer[] = [];
  private readonly slots = new Map<number, Slot>();
  private fileLen = 0;
  private chain: Promise<unknown> = Promise.resolve();
  private closed = false;

  private constructor(path: string, fsync: boolean) {
    this.path = path;
    this.fsync = fsync;
    this.fd = openSync(path, existsSync(path) ? 'r+' : 'w+');
    const image: Buffer = readFileSync(this.fd);
    if (image.length > 0) {
      for (const view of scanFrames(image)) {
        this.slots.set(Number(view.index), {
          arena: image,
          offset: view.offset,
          frameLen: view.frameLen,
          fileOffset: view.offset,
          term: view.term,
        });
      }
      this.arenas.push(image);
    }
    this.fileLen = image.length;
  }

  static open(path: string, opts: { fsync?: boolean } = {}): LogStore {
    return new LogStore(path, opts.fsync ?? true);
  }

  get isClosed(): boolean {
    return this.closed;
  }

  get bytes(): number {
    return this.fileLen;
  }

  get lastIndex(): number {
    let max = 0;
    for (const i of this.slots.keys()) if (i > max) max = i;
    return max;
  }

  get lastTerm(): bigint {
    const s = this.slots.get(this.lastIndex);
    return s ? s.term : 0n;
  }

  at(index: number): EntryView | undefined {
    const s = this.slots.get(index);
    if (!s) return undefined;
    return decodeEntryView(s.arena, s.offset);
  }

  has(index: number): boolean {
    return this.slots.has(index);
  }

  *entries(from: number, to: number): Generator<EntryView> {
    for (let i = from; i <= to; i++) {
      const v = this.at(i);
      if (v) yield v;
    }
  }

  append(input: EntryInput): Promise<EntryView> {
    return this.enqueue(() => {
      const frame = encodeEntry(input);
      writeSync(this.fd, frame, 0, frame.length, this.fileLen);
      if (this.fsync) fsyncSync(this.fd);
      const slot: Slot = {
        arena: frame,
        offset: 0,
        frameLen: frame.length,
        fileOffset: this.fileLen,
        term: BigInt(input.term),
      };
      this.fileLen += frame.length;
      this.arenas.push(frame);
      this.slots.set(Number(input.index), slot);
      return decodeEntryView(frame, 0);
    });
  }

  truncateSuffix(fromIndex: number): Promise<void> {
    return this.enqueue(() => {
      const first = this.slots.get(fromIndex);
      const cut = first ? first.fileOffset : this.fileLen;
      for (const key of [...this.slots.keys()]) {
        if (key >= fromIndex) this.slots.delete(key);
      }
      if (cut < this.fileLen) {
        ftruncateSync(this.fd, cut);
        if (this.fsync) fsyncSync(this.fd);
        this.fileLen = cut;
      }
    });
  }

  close(): Promise<void> {
    if (this.closed) return Promise.resolve();
    return this.enqueue(() => {
      closeSync(this.fd);
      this.closed = true;
    });
  }

  private enqueue<T>(fn: () => T): Promise<T> {
    if (this.closed) return Promise.reject(new Error(`LogStore ${this.path} is closed`));
    const p = this.chain.then(fn);
    this.chain = p.catch(() => {});
    return p;
  }
}

export function fdIsOpen(store: LogStore): boolean {
  try {
    fstatSync((store as unknown as { fd: number }).fd);
    return true;
  } catch {
    return false;
  }
}
