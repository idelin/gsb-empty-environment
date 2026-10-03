import { canonicalize, hashCanonical, sha256Hex } from "./hash.ts";
import { MerkleTree, type MerkleProofStep } from "./merkle.ts";
import { computeContactIntervals } from "./intersection.ts";
import type { TrajectoryPoint } from "./types.ts";

/** 轨迹承诺：对整条轨迹的 Merkle 根，先行公开即不可篡改。 */
export interface TrajectoryCommitment {
  entityId: string;
  pointCount: number;
  firstTime: number;
  lastTime: number;
  merkleRoot: string;
}

export interface CommittedPoint extends TrajectoryPoint {
  index: number;
  hash: string;
  merkleProof: MerkleProofStep[];
}

export interface ContactCertificate {
  certificateId: string;
  entityA: string;
  entityB: string;
  thresholdMeters: number;
  interval: { tStart: number; tEnd: number; durationMs: number };
  minDistanceMeters: number;
  closestApproachTime: number;
  location: { label: string; x: number; y: number; z: number };
  commitmentA: TrajectoryCommitment;
  commitmentB: TrajectoryCommitment;
  witnesses: { entityId: string; points: CommittedPoint[] }[];
  previousHash: string;
  hash: string;
}

export function hashPoint(p: TrajectoryPoint): string {
  return hashCanonical({
    t: p.t,
    x: p.x,
    y: p.y,
    z: p.z,
    label: p.label ?? null,
  });
}

export interface CommittedTrajectory {
  commitment: TrajectoryCommitment;
  tree: MerkleTree;
  points: TrajectoryPoint[];
}

/** 对一条轨迹生成承诺（Merkle 根）。 */
export function commitTrajectory(
  entityId: string,
  points: TrajectoryPoint[],
): CommittedTrajectory {
  const sorted = [...points].sort((a, b) => a.t - b.t);
  const tree = new MerkleTree(sorted.map(hashPoint));
  return {
    commitment: {
      entityId,
      pointCount: sorted.length,
      firstTime: sorted.length ? sorted[0].t : 0,
      lastTime: sorted.length ? sorted[sorted.length - 1].t : 0,
      merkleRoot: tree.root,
    },
    tree,
    points: sorted,
  };
}

/** 选取时段 [tStart, tEnd] 的见证点：时段内全部原始点，外加两侧各一个包夹点。 */
export function selectWitnesses(
  committed: CommittedTrajectory,
  tStart: number,
  tEnd: number,
): CommittedPoint[] {
  const pts = committed.points;
  let before = -1;
  let after = -1;
  let firstInside = -1;
  let lastInside = -1;
  for (let i = 0; i < pts.length; i++) {
    if (pts[i].t < tStart) {
      before = i;
    } else if (pts[i].t > tEnd) {
      after = i;
      break;
    } else {
      if (firstInside < 0) firstInside = i;
      lastInside = i;
    }
  }
  let from = firstInside;
  let to = lastInside;
  if (before >= 0) from = from < 0 ? before : Math.min(from, before);
  if (after >= 0) to = to < 0 ? after : Math.max(to, after);
  const out: CommittedPoint[] = [];
  if (from < 0 || to < 0) return out;
  for (let i = from; i <= to; i++) {
    out.push({
      ...pts[i],
      index: i,
      hash: hashPoint(pts[i]),
      merkleProof: committed.tree.proof(i),
    });
  }
  return out;
}

export function certificateBodyHash(cert: ContactCertificate): string {
  const { certificateId: _id, hash: _h, ...body } = cert;
  return hashCanonical(body);
}

export function sealCertificate(
  cert: Omit<ContactCertificate, "certificateId" | "hash">,
): ContactCertificate {
  const bodyHash = hashCanonical(cert);
  const hash = sha256Hex(`${cert.previousHash}|${bodyHash}`);
  return { ...cert, certificateId: hash, hash };
}

export interface VerificationResult {
  ok: boolean;
  errors: string[];
}

const INTERVAL_TOLERANCE_MS = 1e-3;
const DISTANCE_TOLERANCE_M = 1e-6;

/** 校验证书：见证点哈希、Merkle 包含证明、链式哈希，并从见证点重算相遇时段。 */
export function verifyCertificate(cert: ContactCertificate): VerificationResult {
  const errors: string[] = [];

  const expectedHash = sha256Hex(`${cert.previousHash}|${certificateBodyHash(cert)}`);
  if (cert.hash !== expectedHash) {
    errors.push("证书链式哈希不匹配（内容或前序哈希被篡改）");
  }
  if (cert.certificateId !== cert.hash) {
    errors.push("certificateId 与证书哈希不一致");
  }

  const commitments = new Map<string, TrajectoryCommitment>([
    [cert.commitmentA.entityId, cert.commitmentA],
    [cert.commitmentB.entityId, cert.commitmentB],
  ]);

  const witnessTrajectories = new Map<string, TrajectoryPoint[]>();
  for (const witness of cert.witnesses) {
    const commitment = commitments.get(witness.entityId);
    if (!commitment) {
      errors.push(`见证实体 ${witness.entityId} 不在证书承诺中`);
      continue;
    }
    const pts: TrajectoryPoint[] = [];
    for (const p of witness.points) {
      const recomputed = hashPoint(p);
      if (recomputed !== p.hash) {
        errors.push(`实体 ${witness.entityId} 见证点 #${p.index} 哈希不匹配`);
        continue;
      }
      if (!MerkleTree.verify(p.hash, p.merkleProof, commitment.merkleRoot)) {
        errors.push(`实体 ${witness.entityId} 见证点 #${p.index} Merkle 证明无效`);
        continue;
      }
      pts.push({ t: p.t, x: p.x, y: p.y, z: p.z, label: p.label });
    }
    pts.sort((a, b) => a.t - b.t);
    witnessTrajectories.set(witness.entityId, pts);
  }

  const trajA = witnessTrajectories.get(cert.entityA);
  const trajB = witnessTrajectories.get(cert.entityB);
  if (trajA && trajB && trajA.length > 0 && trajB.length > 0) {
    const recomputed = computeContactIntervals(trajA, trajB, cert.thresholdMeters);
    const match = recomputed.find(
      (iv) =>
        Math.abs(iv.tStart - cert.interval.tStart) <= INTERVAL_TOLERANCE_MS &&
        Math.abs(iv.tEnd - cert.interval.tEnd) <= INTERVAL_TOLERANCE_MS,
    );
    if (!match) {
      errors.push("由见证点重算的相遇时段与证书声明不一致");
    } else if (Math.abs(match.minDistance - cert.minDistanceMeters) > DISTANCE_TOLERANCE_M) {
      errors.push("由见证点重算的最近距离与证书声明不一致");
    }
  } else {
    errors.push("见证点不足，无法重算相遇时段");
  }

  return { ok: errors.length === 0, errors };
}
