import { Hash } from "./hash";
import { ChunkSource } from "./source";
import {
  ChunkProof,
  CommitmentHeader,
  chunkCount,
  chunkDataLength,
  commitmentMatches,
  hashLeaf,
  verifyProof,
} from "./tree";

/**
 * Per-chunk verdicts:
 * - "ok"           data + canonical proof recompute to the trusted root
 * - "bad"          data present, evidence well-formed, but contradicts the root
 * - "missing"      data was never received — NOT evidence of corruption
 * - "insufficient" data present but proof absent or non-canonical, or the
 *                  commitment header itself does not check out
 */
export type ChunkStatus = "ok" | "bad" | "missing" | "insufficient";

export interface RangeReport {
  /** False when the supplied header does not match the trusted commitment. */
  commitmentValid: boolean;
  start: number;
  end: number;
  status: Map<number, ChunkStatus>;
  ok: number[];
  bad: number[];
  missing: number[];
  insufficient: number[];
}

export interface VerifyRangeParams {
  /** Trusted commitment (obtained out-of-band). */
  commitment: Hash;
  /** Claimed header; verified against the commitment before anything else. */
  header: CommitmentHeader;
  /** Chunk range [start, end) to verify. */
  start: number;
  end: number;
  /** Received chunk data (only requested indices are ever pulled). */
  chunks: ChunkSource;
  /** Received proofs, keyed by chunk index. */
  proofs: ReadonlyMap<number, ChunkProof>;
}

export async function verifyRange(params: VerifyRangeParams): Promise<RangeReport> {
  const { commitment, header, chunks, proofs } = params;
  const total = chunkCount(header.fileLength, header.chunkSize);
  const start = Math.max(0, params.start);
  const end = Math.min(params.end, total);

  const status = new Map<number, ChunkStatus>();
  const report: RangeReport = {
    commitmentValid: commitmentMatches(header, commitment),
    start,
    end,
    status,
    ok: [],
    bad: [],
    missing: [],
    insufficient: [],
  };

  for (let index = start; index < end; index++) {
    let verdict: ChunkStatus;

    if (!report.commitmentValid) {
      // Header/commitment mismatch (e.g. tampered length or chunk size):
      // nothing can be proven, but absence of proof is not corruption.
      const data = await chunks.getChunk(index);
      verdict = data === undefined ? "missing" : "insufficient";
    } else {
      const data = await chunks.getChunk(index);
      if (data === undefined) {
        verdict = "missing"; // unreceived is not "corrupt"
      } else {
        const expectedLength = chunkDataLength(header.fileLength, header.chunkSize, index);
        const proof = proofs.get(index);
        if (data.length !== expectedLength) {
          verdict = "bad"; // wrong-sized chunk can never match the committed leaf
        } else if (proof === undefined || proof.index !== index) {
          verdict = "insufficient";
        } else {
          const leaf = hashLeaf(header.fileLength, header.chunkSize, index, data);
          const check = verifyProof(total, header.treeRoot, leaf, proof);
          if (check.ok) {
            verdict = "ok";
          } else {
            verdict = check.reason === "malformed" ? "insufficient" : "bad";
          }
        }
      }
    }

    status.set(index, verdict);
    report[verdict].push(index);
  }

  return report;
}
