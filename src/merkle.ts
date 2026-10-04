import { createHash } from 'node:crypto';

export type Hash = Buffer;

export const LEAF_DOMAIN = 0x00;
export const NODE_DOMAIN = 0x01;
export const COMMITMENT_DOMAIN = 0x02;
export const EMPTY_ROOT_DOMAIN = 0x03;

export function sha256(...parts: Buffer[]): Hash {
  const h = createHash('sha256');
  for (const p of parts) h.update(p);
  return h.digest();
}

function u64be(n: number): Buffer {
  const b = Buffer.alloc(8);
  b.writeBigUInt64BE(BigInt(n));
  return b;
}

function u32be(n: number): Buffer {
  const b = Buffer.alloc(4);
  b.writeUInt32BE(n);
  return b;
}

/** Number of leaves (chunks) for a file. Empty file => 0 leaves. */
export function numLeaves(fileLength: number, chunkSize: number): number {
  if (fileLength < 0 || chunkSize <= 0) throw new RangeError('bad fileLength/chunkSize');
  return Math.ceil(fileLength / chunkSize);
}

/** Fixed padding rule: the final chunk is zero-padded up to chunkSize. */
export function padBlock(data: Buffer, chunkSize: number): Buffer {
  if (data.length > chunkSize) throw new RangeError('block larger than chunkSize');
  if (data.length === chunkSize) return data;
  return Buffer.concat([data, Buffer.alloc(chunkSize - data.length)]);
}

/**
 * Leaf hash binds file length, chunk size and chunk index, so a proof
 * cannot be replayed against a different file shape or position.
 * `data` must already be padded to chunkSize.
 */
export function leafHash(
  fileLength: number,
  chunkSize: number,
  chunkIndex: number,
  data: Buffer,
): Hash {
  return sha256(
    Buffer.from([LEAF_DOMAIN]),
    u64be(fileLength),
    u32be(chunkSize),
    u64be(chunkIndex),
    data,
  );
}

export function parentHash(left: Hash, right: Hash): Hash {
  return sha256(Buffer.from([NODE_DOMAIN]), left, right);
}

/** Root of a file with zero chunks. */
export function emptyRoot(): Hash {
  return sha256(Buffer.from([EMPTY_ROOT_DOMAIN]));
}

/**
 * The tree root (commitment) binds the file length and chunk size on top
 * of the Merkle root, so metadata cannot be altered without detection.
 */
export function commitmentFor(fileLength: number, chunkSize: number, merkleRoot: Hash): Hash {
  return sha256(
    Buffer.from([COMMITMENT_DOMAIN]),
    u64be(fileLength),
    u32be(chunkSize),
    merkleRoot,
  );
}

/**
 * Tree shape rule: at each level nodes are paired left-to-right; an odd
 * trailing node is promoted to the next level unchanged (no rehash, no
 * duplication). This is deterministic given the leaf count.
 */
export function buildLevels(leaves: Hash[]): Hash[][] {
  const levels: Hash[][] = [];
  if (leaves.length === 0) return levels;
  let current = leaves;
  levels.push(current);
  while (current.length > 1) {
    const next: Hash[] = [];
    for (let i = 0; i + 1 < current.length; i += 2) {
      next.push(parentHash(current[i], current[i + 1]));
    }
    if (current.length % 2 === 1) next.push(current[current.length - 1]);
    current = next;
    levels.push(current);
  }
  return levels;
}

export function merkleRootOf(levels: Hash[][]): Hash {
  if (levels.length === 0) return emptyRoot();
  return levels[levels.length - 1][0];
}
