import { createHash } from 'node:crypto';

export function sha256(data: Buffer | string): Buffer {
  return createHash('sha256').update(data).digest();
}

export function hashLeaf(data: Buffer | string): Buffer {
  return sha256(Buffer.concat([Buffer.from([0x00]), Buffer.isBuffer(data) ? data : Buffer.from(data)]));
}

function hashNode(left: Buffer, right: Buffer): Buffer {
  return sha256(Buffer.concat([Buffer.from([0x01]), left, right]));
}

export interface ProofStep {
  position: 'left' | 'right';
  hash: string; // hex
}

export class MerkleTree {
  readonly leaves: Buffer[];
  private readonly layers: Buffer[][];

  constructor(items: Array<Buffer | string>) {
    if (items.length === 0) throw new Error('MerkleTree requires at least one item');
    this.leaves = items.map(hashLeaf);
    this.layers = [this.leaves];
    let level = this.leaves;
    while (level.length > 1) {
      const next: Buffer[] = [];
      for (let i = 0; i < level.length; i += 2) {
        const left = level[i];
        const right = i + 1 < level.length ? level[i + 1] : level[i];
        next.push(hashNode(left, right));
      }
      this.layers.push(next);
      level = next;
    }
  }

  get size(): number {
    return this.leaves.length;
  }

  get root(): Buffer {
    return this.layers[this.layers.length - 1][0];
  }

  leafHash(index: number): Buffer {
    if (index < 0 || index >= this.leaves.length) throw new RangeError(`leaf index ${index} out of range`);
    return this.leaves[index];
  }

  getProof(index: number): ProofStep[] {
    if (index < 0 || index >= this.leaves.length) throw new RangeError(`leaf index ${index} out of range`);
    const proof: ProofStep[] = [];
    let i = index;
    for (let depth = 0; depth < this.layers.length - 1; depth++) {
      const level = this.layers[depth];
      const sibling = i % 2 === 0 ? i + 1 : i - 1;
      // odd-length level: last node is paired with itself
      const siblingHash = sibling < level.length ? level[sibling] : level[i];
      proof.push({
        position: i % 2 === 0 ? 'right' : 'left',
        hash: siblingHash.toString('hex'),
      });
      i = Math.floor(i / 2);
    }
    return proof;
  }

  static verifyProof(leafHashHex: string, proof: ProofStep[], expectedRootHex: string): boolean {
    let acc = Buffer.from(leafHashHex, 'hex');
    for (const step of proof) {
      const sibling = Buffer.from(step.hash, 'hex');
      acc = step.position === 'right' ? hashNode(acc, sibling) : hashNode(sibling, acc);
    }
    return acc.toString('hex') === expectedRootHex;
  }
}
