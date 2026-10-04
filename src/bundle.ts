import { Hash, fromHex, toHex } from "./hash";
import { ChunkProof, CommitmentHeader } from "./tree";

/** Wire format for transporting a partial verification bundle. */
export interface ProofBundle {
  header: CommitmentHeader;
  commitment: Hash;
  /** Received chunk data, keyed by chunk index. Absent indices are "missing". */
  chunks: Map<number, Uint8Array>;
  proofs: Map<number, ChunkProof>;
}

interface BundleJson {
  header: { fileLength: number; chunkSize: number; treeRoot: string };
  commitment: string;
  chunks: Record<string, string>; // index -> base64
  proofs: Record<string, { index: number; siblings: string[] }>;
}

export function bundleToJson(bundle: ProofBundle): string {
  const chunks: Record<string, string> = {};
  for (const [i, data] of bundle.chunks) {
    chunks[String(i)] = Buffer.from(data).toString("base64");
  }
  const proofs: BundleJson["proofs"] = {};
  for (const [i, p] of bundle.proofs) {
    proofs[String(i)] = { index: p.index, siblings: p.siblings.map(toHex) };
  }
  return JSON.stringify({
    header: {
      fileLength: bundle.header.fileLength,
      chunkSize: bundle.header.chunkSize,
      treeRoot: toHex(bundle.header.treeRoot),
    },
    commitment: toHex(bundle.commitment),
    chunks,
    proofs,
  } satisfies BundleJson);
}

export function bundleFromJson(json: string): ProofBundle {
  const raw = JSON.parse(json) as BundleJson;
  const chunks = new Map<number, Uint8Array>();
  for (const [k, v] of Object.entries(raw.chunks)) {
    chunks.set(Number(k), new Uint8Array(Buffer.from(v, "base64")));
  }
  const proofs = new Map<number, ChunkProof>();
  for (const [k, v] of Object.entries(raw.proofs)) {
    proofs.set(Number(k), { index: v.index, siblings: v.siblings.map(fromHex) });
  }
  return {
    header: {
      fileLength: raw.header.fileLength,
      chunkSize: raw.header.chunkSize,
      treeRoot: fromHex(raw.header.treeRoot),
    },
    commitment: fromHex(raw.commitment),
    chunks,
    proofs,
  };
}
