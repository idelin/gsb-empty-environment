import { performance } from "node:perf_hooks";
import type { SpacetimeGridIndex } from "./grid_index.ts";
import { computeContactIntervals, positionAt } from "./intersection.ts";
import {
  commitTrajectory,
  sealCertificate,
  selectWitnesses,
  verifyCertificate,
  type ContactCertificate,
  type CommittedTrajectory,
  type TrajectoryCommitment,
} from "./certificate.ts";
import { hashCanonical, sha256Hex } from "./hash.ts";
import type { ContactInterval, TrajectoryPoint } from "./types.ts";

export interface IntersectionProof {
  query: {
    entityA: string;
    entityB: string;
    thresholdMeters: number;
    generatedAt: string;
  };
  commitments: TrajectoryCommitment[];
  intervals: ContactInterval[];
  certificates: ContactCertificate[];
  /** 全部证书链式哈希的最终摘要，可对外公开存证。 */
  proofDigest: string;
  /** 求交计算耗时（不含证书生成）。 */
  queryDurationMs: number;
}

export interface ProofVerification {
  ok: boolean;
  errors: string[];
  certificateResults: { certificateId: string; ok: boolean; errors: string[] }[];
}

export class IntersectionProver {
  private readonly index: SpacetimeGridIndex;
  /** 轨迹承诺缓存：承诺先行公开存证，无需每次查询重算 Merkle 树。 */
  private readonly commitmentCache = new Map<
    string,
    { signature: string; committed: CommittedTrajectory }
  >();

  constructor(index: SpacetimeGridIndex) {
    this.index = index;
  }

  /** 轨迹入库时预先提交承诺（现实部署中承诺先行公开存证，查询时直接复用）。 */
  precommit(entityIds?: Iterable<string>): void {
    const ids = entityIds ?? this.index.entityIds();
    for (const id of ids) this.committedTrajectory(id);
  }

  private committedTrajectory(entityId: string): CommittedTrajectory {
    const points = this.index.trajectoryOf(entityId);
    const signature = `${points.length}:${points[0]?.t ?? 0}:${points[points.length - 1]?.t ?? 0}`;
    const cached = this.commitmentCache.get(entityId);
    if (cached && cached.signature === signature) return cached.committed;
    const committed = commitTrajectory(entityId, points);
    this.commitmentCache.set(entityId, { signature, committed });
    return committed;
  }

  /** 证明实体 A 与实体 B 是否曾在历史某时空相距小于 thresholdMeters。 */
  prove(entityA: string, entityB: string, thresholdMeters: number): IntersectionProof {
    const queryStart = performance.now();

    // 1) 反向索引预筛：只保留两实体网格邻域相交的时间切片。
    const slices = this.index.candidateSlices(entityA, entityB, thresholdMeters);
    // 2) 在候选切片内做精确时空求交（分段线性 + 二次求根）。
    const ptsA = this.index.pointsInSlices(entityA, slices);
    const ptsB = this.index.pointsInSlices(entityB, slices);
    const raw = computeContactIntervals(ptsA, ptsB, thresholdMeters);
    const queryDurationMs = performance.now() - queryStart;

    // 3) 丰富化为带位置证书的相遇时段。
    const committedA = this.committedTrajectory(entityA);
    const committedB = this.committedTrajectory(entityB);
    const fullA = committedA.points;
    const fullB = committedB.points;
    const intervals: ContactInterval[] = raw.map((iv) => ({
      tStart: iv.tStart,
      tEnd: iv.tEnd,
      durationMs: iv.tEnd - iv.tStart,
      minDistanceMeters: iv.minDistance,
      tClosest: iv.tClosest,
      location: locateAt(fullA, fullB, iv.tClosest),
    }));

    // 4) 生成不可抵赖证书：轨迹承诺 + 见证点 Merkle 证明 + 链式哈希。
    const query = {
      entityA,
      entityB,
      thresholdMeters,
      generatedAt: new Date().toISOString(),
    };
    let previousHash = genesisHash(query);
    const certificates: ContactCertificate[] = intervals.map((iv, i) => {
      const sealed = sealCertificate({
        entityA,
        entityB,
        thresholdMeters,
        interval: { tStart: iv.tStart, tEnd: iv.tEnd, durationMs: iv.durationMs },
        minDistanceMeters: iv.minDistanceMeters,
        closestApproachTime: iv.tClosest,
        location: iv.location,
        commitmentA: committedA.commitment,
        commitmentB: committedB.commitment,
        witnesses: [
          { entityId: entityA, points: selectWitnesses(committedA, iv.tStart, iv.tEnd) },
          { entityId: entityB, points: selectWitnesses(committedB, iv.tStart, iv.tEnd) },
        ],
        previousHash,
      });
      previousHash = sealed.hash;
      void i;
      return sealed;
    });

    return {
      query,
      commitments: [committedA.commitment, committedB.commitment],
      intervals,
      certificates,
      proofDigest: previousHash,
      queryDurationMs,
    };
  }
}

export function genesisHash(query: unknown): string {
  return sha256Hex(`SPACETIME-PROOF-GENESIS|${hashCanonical(query)}`);
}

/** 独立验证一份相交证明：链式结构 + 每张证书密码学校验。 */
export function verifyProof(proof: IntersectionProof): ProofVerification {
  const errors: string[] = [];
  const certificateResults: ProofVerification["certificateResults"] = [];

  let expectedPrevious = genesisHash(proof.query);
  for (const cert of proof.certificates) {
    if (cert.previousHash !== expectedPrevious) {
      errors.push(`证书 ${cert.certificateId} 的前序哈希断裂`);
    }
    const result = verifyCertificate(cert);
    certificateResults.push({
      certificateId: cert.certificateId,
      ok: result.ok,
      errors: result.errors,
    });
    if (!result.ok) errors.push(...result.errors);
    expectedPrevious = cert.hash;
  }
  if (proof.certificates.length === 0) {
    if (proof.proofDigest !== expectedPrevious) {
      errors.push("空证明的摘要与创世哈希不一致");
    }
  } else if (proof.proofDigest !== expectedPrevious) {
    errors.push("证明摘要与证书链末端不一致");
  }
  return { ok: errors.length === 0, errors, certificateResults };
}

/** 相遇时刻的位置：两实体位置中点 + 时间上最近的轨迹点标签。 */
function locateAt(
  a: TrajectoryPoint[],
  b: TrajectoryPoint[],
  t: number,
): { label: string; x: number; y: number; z: number } {
  const pa = positionAt(a, t);
  const pb = positionAt(b, t);
  const label = nearestLabel(a, t) ?? nearestLabel(b, t) ?? "（无标签位置）";
  return {
    label,
    x: (pa.x + pb.x) / 2,
    y: (pa.y + pb.y) / 2,
    z: (pa.z + pb.z) / 2,
  };
}

function nearestLabel(points: TrajectoryPoint[], t: number): string | null {
  let best: string | null = null;
  let bestDt = Infinity;
  for (const p of points) {
    if (p.label === undefined) continue;
    const dt = Math.abs(p.t - t);
    if (dt < bestDt) {
      bestDt = dt;
      best = p.label;
    }
  }
  return best;
}
