import { createHash } from 'node:crypto';
import { canonical } from './stable-json.ts';
import type { ContactInterval, Trajectory } from './types.ts';

export function sha256Hex(data: string): string {
  return createHash('sha256').update(data, 'utf8').digest('hex');
}

/** 对轨迹做哈希承诺：证明绑定到确定的输入数据，事后不可替换。 */
export function trajectoryDigest(traj: Trajectory): string {
  return sha256Hex(canonical({ entityId: traj.entityId, points: traj.points }));
}

/** 单份相交地点证书。hash 链接前一份证书，形成防篡改链。 */
export interface LocationCertificate {
  seq: number;
  entityA: string;
  entityB: string;
  start: number;
  end: number;
  minDistance: number;
  minDistanceAt: number;
  location: { x: number; y: number; floor: number | null };
  zone: string | null;
  prevHash: string;
  hash: string;
}

/** 完整证明：输入承诺 + 证书链 + 根哈希。 */
export interface IntersectionProof {
  engine: string;
  version: number;
  thresholdMeters: number;
  generatedAt: number;
  trajectoryDigests: Record<string, string>;
  certificates: LocationCertificate[];
  rootHash: string;
}

const GENESIS = 'GENESIS';

function certificatePayload(cert: Omit<LocationCertificate, 'hash'>): string {
  return canonical(cert);
}

export function issueCertificates(
  entityA: string,
  entityB: string,
  intervals: ContactInterval[],
  locations: Array<{ x: number; y: number; floor: number | null; zone: string | null }>,
): LocationCertificate[] {
  const certs: LocationCertificate[] = [];
  let prevHash = GENESIS;
  intervals.forEach((interval, i) => {
    const body = {
      seq: i,
      entityA,
      entityB,
      start: interval.start,
      end: interval.end,
      minDistance: interval.minDistance,
      minDistanceAt: interval.minDistanceAt,
      location: locations[i],
      zone: locations[i].zone,
      prevHash,
    };
    const hash = sha256Hex(certificatePayload(body));
    certs.push({ ...body, hash });
    prevHash = hash;
  });
  return certs;
}

export function buildProof(
  thresholdMeters: number,
  trajectories: Trajectory[],
  certificates: LocationCertificate[],
  generatedAt: number,
): IntersectionProof {
  const trajectoryDigests: Record<string, string> = {};
  for (const traj of trajectories) {
    trajectoryDigests[traj.entityId] = trajectoryDigest(traj);
  }
  const chainTip = certificates.length > 0 ? certificates[certificates.length - 1].hash : GENESIS;
  const rootHash = sha256Hex(canonical({ thresholdMeters, trajectoryDigests, chainTip }));
  return {
    engine: 'spacetime-intersection-prover',
    version: 1,
    thresholdMeters,
    generatedAt,
    trajectoryDigests,
    certificates,
    rootHash,
  };
}

export interface VerifyResult {
  ok: boolean;
  errors: string[];
}

/** 独立验证：重放哈希链与输入承诺，任何字段被篡改都会失败。 */
export function verifyProof(proof: IntersectionProof, trajectories: Trajectory[]): VerifyResult {
  const errors: string[] = [];

  for (const traj of trajectories) {
    const committed = proof.trajectoryDigests[traj.entityId];
    if (committed === undefined) {
      errors.push(`轨迹 ${traj.entityId} 缺少输入承诺`);
    } else if (committed !== trajectoryDigest(traj)) {
      errors.push(`轨迹 ${traj.entityId} 与证明中的输入承诺不一致`);
    }
  }

  let prevHash = GENESIS;
  proof.certificates.forEach((cert, i) => {
    if (cert.seq !== i) errors.push(`证书 #${i} 序号断裂`);
    if (cert.prevHash !== prevHash) errors.push(`证书 #${i} 前向链接断裂`);
    const { hash, ...body } = cert;
    if (sha256Hex(certificatePayload(body)) !== hash) {
      errors.push(`证书 #${i} 内容哈希不匹配（疑似篡改）`);
    }
    prevHash = cert.hash;
  });

  const chainTip = proof.certificates.length > 0
    ? proof.certificates[proof.certificates.length - 1].hash
    : GENESIS;
  const expectedRoot = sha256Hex(canonical({
    thresholdMeters: proof.thresholdMeters,
    trajectoryDigests: proof.trajectoryDigests,
    chainTip,
  }));
  if (expectedRoot !== proof.rootHash) errors.push('根哈希不匹配');

  return { ok: errors.length === 0, errors };
}
