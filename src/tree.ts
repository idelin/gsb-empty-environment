import { openSync, readSync, fstatSync, closeSync, type PathLike } from 'node:fs';
import {
  buildLevels,
  commitmentFor,
  leafHash,
  merkleRootOf,
  numLeaves,
  padBlock,
  type Hash,
} from './merkle.ts';

/** Random-access byte source. The prover only reads the ranges it needs. */
export interface RandomAccessReader {
  readonly byteLength: number;
  read(offset: number, length: number): Buffer;
}

export class BufferReader implements RandomAccessReader {
  readonly byteLength: number;
  private readonly buf: Buffer;
  constructor(buf: Buffer) {
    this.buf = buf;
    this.byteLength = buf.length;
  }
  read(offset: number, length: number): Buffer {
    return this.buf.subarray(offset, offset + length);
  }
}

export class FileReader implements RandomAccessReader {
  readonly byteLength: number;
  private readonly fd: number;
  constructor(path: PathLike) {
    this.fd = openSync(path, 'r');
    this.byteLength = fstatSync(this.fd).size;
  }
  read(offset: number, length: number): Buffer {
    const buf = Buffer.alloc(length);
    let got = 0;
    while (got < length) {
      const n = readSync(this.fd, buf, got, length - got, offset + got);
      if (n === 0) break;
      got += n;
    }
    return buf.subarray(0, got);
  }
  close(): void {
    closeSync(this.fd);
  }
}

export interface ProofNode {
  level: number;
  index: number;
  hash: string; // hex
}

/** Serializable proof covering a set of leaf indices. */
export interface RangeProof {
  format: 'hash-tree-proof/1';
  fileLength: number;
  chunkSize: number;
  leafIndices: number[];
  nodes: ProofNode[];
}

export interface RangeData {
  proof: RangeProof;
  chunks: Map<number, Buffer>;
}

/**
 * A published hash tree. Building it reads the whole file once (publish
 * time); afterwards range proofs are served reading only the requested
 * chunks from the reader.
 */
export class HashTree {
  readonly fileLength: number;
  readonly chunkSize: number;
  private readonly levels: Hash[][];

  private constructor(fileLength: number, chunkSize: number, levels: Hash[][]) {
    this.fileLength = fileLength;
    this.chunkSize = chunkSize;
    this.levels = levels;
  }

  static build(reader: RandomAccessReader, chunkSize: number): HashTree {
    if (!Number.isSafeInteger(chunkSize) || chunkSize <= 0) {
      throw new RangeError('chunkSize must be a positive integer');
    }
    const fileLength = reader.byteLength;
    const count = numLeaves(fileLength, chunkSize);
    const leaves: Hash[] = [];
    for (let i = 0; i < count; i++) {
      const offset = i * chunkSize;
      const len = Math.min(chunkSize, fileLength - offset);
      const data = padBlock(reader.read(offset, len), chunkSize);
      leaves.push(leafHash(fileLength, chunkSize, i, data));
    }
    return new HashTree(fileLength, chunkSize, buildLevels(leaves));
  }

  /** Rebuild a tree from serialized levels (hex), e.g. saved at publish time. */
  static fromLevels(fileLength: number, chunkSize: number, hexLevels: string[][]): HashTree {
    return new HashTree(
      fileLength,
      chunkSize,
      hexLevels.map((lvl) => lvl.map((h) => Buffer.from(h, 'hex'))),
    );
  }

  toLevelsHex(): string[][] {
    return this.levels.map((lvl) => lvl.map((h) => h.toString('hex')));
  }

  get root(): Hash {
    return merkleRootOf(this.levels);
  }

  /** The tree root: binds file length, chunk size and Merkle root. */
  get commitment(): Hash {
    return commitmentFor(this.fileLength, this.chunkSize, this.root);
  }

  get leafCount(): number {
    return numLeaves(this.fileLength, this.chunkSize);
  }

  /**
   * Produce a proof plus the chunk data for byte range [start, end).
   * Only the chunks overlapping the range are read from `reader`.
   */
  proveRange(reader: RandomAccessReader, start: number, end: number): RangeData {
    if (reader.byteLength !== this.fileLength) {
      throw new Error('reader length does not match tree fileLength');
    }
    const first = Math.floor(start / this.chunkSize);
    const last = Math.floor((end - 1) / this.chunkSize);
    if (first < 0 || last >= this.leafCount || start >= end) {
      throw new RangeError(`range [${start}, ${end}) outside file of ${this.fileLength} bytes`);
    }
    const leafIndices: number[] = [];
    for (let i = first; i <= last; i++) leafIndices.push(i);

    const seen = new Map<string, ProofNode>();
    for (const leaf of leafIndices) {
      let idx = leaf;
      for (let level = 0; level < this.levels.length - 1; level++) {
        const size = this.levels[level].length;
        const sib = idx ^ 1;
        if (sib < size) {
          const key = `${level}:${sib}`;
          if (!seen.has(key)) {
            seen.set(key, { level, index: sib, hash: this.levels[level][sib].toString('hex') });
          }
        }
        idx >>= 1;
      }
    }

    const chunks = new Map<number, Buffer>();
    for (const i of leafIndices) {
      const offset = i * this.chunkSize;
      const len = Math.min(this.chunkSize, this.fileLength - offset);
      chunks.set(i, reader.read(offset, len));
    }

    return {
      proof: {
        format: 'hash-tree-proof/1',
        fileLength: this.fileLength,
        chunkSize: this.chunkSize,
        leafIndices,
        nodes: [...seen.values()],
      },
      chunks,
    };
  }
}
