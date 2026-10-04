import {
  commitmentFor,
  leafHash,
  numLeaves,
  padBlock,
  parentHash,
  type Hash,
} from './merkle.ts';
import type { RangeProof } from './tree.ts';

export type ChunkStatus = 'verified' | 'corrupt' | 'missing' | 'insufficient-evidence';

export interface ChunkReport {
  index: number;
  status: ChunkStatus;
  detail?: string;
}

export interface RangeReport {
  start: number;
  end: number;
  fileLength: number;
  chunkSize: number;
  ok: boolean;
  chunks: ChunkReport[];
}

export interface VerifyRangeInput {
  /** Trusted tree root obtained from the publisher. */
  commitment: Hash;
  /** Trusted metadata distributed alongside the commitment. */
  fileLength: number;
  chunkSize: number;
  /** Requested byte range [start, end). */
  start: number;
  end: number;
  /** Chunks received for the range, keyed by chunk index. */
  chunks: ReadonlyMap<number, Buffer>;
  /** Proof received for the range, or null if none arrived. */
  proof: RangeProof | null;
}

/**
 * Verify a byte range using only the received chunks and proof.
 *
 * The verifier is a pure function: it never touches the file or any
 * reader, so it cannot read more than the evidence it is given.
 *
 * Status semantics:
 * - `verified`              chunk data + proof recompute to the trusted root
 * - `corrupt`               chunk present, proof structurally valid, but the
 *                           recomputed root differs from the trusted root
 * - `missing`               chunk in the requested range was not received
 *                           (never reported as corrupt)
 * - `insufficient-evidence` chunk present but the proof is absent, malformed
 *                           (e.g. duplicated path nodes) or incomplete
 */
export function verifyRange(input: VerifyRangeInput): RangeReport {
  const { commitment, fileLength, chunkSize, chunks, proof } = input;
  const end = Math.min(input.end, fileLength);
  const start = Math.min(input.start, end);

  const reports: ChunkReport[] = [];
  const leafIndices: number[] = [];
  if (end > start) {
    const first = Math.floor(start / chunkSize);
    const last = Math.floor((end - 1) / chunkSize);
    for (let i = first; i <= last; i++) leafIndices.push(i);
  }

  for (const i of leafIndices) {
    if (!chunks.has(i)) {
      reports.push({ index: i, status: 'missing', detail: 'chunk not received' });
    }
  }

  const structuralError = checkProofStructure(proof, fileLength, chunkSize, leafIndices);
  const nodeMap = structuralError === null ? buildNodeMap(proof!) : null;

  for (const i of leafIndices) {
    const data = chunks.get(i);
    if (data === undefined) continue; // already reported missing

    if (structuralError !== null) {
      reports.push({ index: i, status: 'insufficient-evidence', detail: structuralError });
      continue;
    }

    const expected = Math.min(chunkSize, fileLength - i * chunkSize);
    if (data.length !== expected && data.length !== chunkSize) {
      reports.push({
        index: i,
        status: 'corrupt',
        detail: `chunk length ${data.length}, expected ${expected}`,
      });
      continue;
    }

    const root = recomputeRoot(fileLength, chunkSize, i, data, nodeMap!);
    if (root === null) {
      reports.push({
        index: i,
        status: 'insufficient-evidence',
        detail: 'proof path incomplete for chunk',
      });
      continue;
    }

    if (commitmentFor(fileLength, chunkSize, root).equals(commitment)) {
      reports.push({ index: i, status: 'verified' });
    } else {
      reports.push({
        index: i,
        status: 'corrupt',
        detail: 'recomputed root does not match trusted commitment',
      });
    }
  }

  reports.sort((a, b) => a.index - b.index);
  return {
    start,
    end,
    fileLength,
    chunkSize,
    ok: reports.every((r) => r.status === 'verified'),
    chunks: reports,
  };
}

function checkProofStructure(
  proof: RangeProof | null,
  fileLength: number,
  chunkSize: number,
  leafIndices: number[],
): string | null {
  if (proof === null) return 'no proof received';
  if (proof.format !== 'hash-tree-proof/1') return 'unknown proof format';
  if (proof.fileLength !== fileLength || proof.chunkSize !== chunkSize) {
    return 'proof metadata does not match trusted fileLength/chunkSize';
  }
  const want = leafIndices.join(',');
  const got = [...proof.leafIndices].sort((a, b) => a - b).join(',');
  if (want !== got) return 'proof does not cover exactly the requested chunks';
  const seen = new Set<string>();
  for (const n of proof.nodes) {
    const key = `${n.level}:${n.index}`;
    if (seen.has(key)) return `duplicate path node ${key}`;
    seen.add(key);
    if (typeof n.hash !== 'string' || !/^[0-9a-f]{64}$/.test(n.hash)) {
      return `malformed hash at node ${key}`;
    }
  }
  return null;
}

function buildNodeMap(proof: RangeProof): Map<string, Hash> {
  const map = new Map<string, Hash>();
  for (const n of proof.nodes) map.set(`${n.level}:${n.index}`, Buffer.from(n.hash, 'hex'));
  return map;
}

/** Recompute the Merkle root from one chunk and its proof path. */
function recomputeRoot(
  fileLength: number,
  chunkSize: number,
  leafIndex: number,
  data: Buffer,
  nodes: Map<string, Hash>,
): Hash | null {
  let h = leafHash(fileLength, chunkSize, leafIndex, padBlock(data, chunkSize));
  let idx = leafIndex;
  let size = numLeaves(fileLength, chunkSize);
  let level = 0;
  while (size > 1) {
    const sib = idx ^ 1;
    if (sib < size) {
      const sibHash = nodes.get(`${level}:${sib}`);
      if (sibHash === undefined) return null;
      h = idx % 2 === 0 ? parentHash(h, sibHash) : parentHash(sibHash, h);
    }
    idx >>= 1;
    size = Math.ceil(size / 2);
    level++;
  }
  return h;
}
