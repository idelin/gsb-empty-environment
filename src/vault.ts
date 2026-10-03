import {
  eciesDecrypt,
  eciesEncrypt,
  hkdf,
  hmacSha256,
  randomKey,
  type EciesBox,
} from './crypto.ts';
import { gridCell, gridHash } from './grid.ts';
import { AuditLog } from './audit-log.ts';
import { verifyCredential, type AuditCredential } from './authority.ts';

export interface VaultConfig {
  authorityPublicKeyPem: string;
  masterSecret?: Buffer;
  epochMs?: number;
  cellSizeDeg?: number;
  auditLogFile?: string;
}

export interface StoredRecord {
  pseudonym: string;
  epoch: number;
  timestamp: number;
  gridHash: string;
  payload: EciesBox;
}

export interface HeatCell {
  gridHash: string;
  count: number;
}

export interface TrajectoryPoint {
  timestamp: number;
  realId: string;
  lat: number;
  lng: number;
}

interface SealedPayload {
  realId: string;
  lat: number;
  lng: number;
}

const DEFAULT_EPOCH_MS = 24 * 60 * 60 * 1000;
const DEFAULT_CELL_SIZE_DEG = 0.01;

export class TrajectoryVault {
  private readonly masterSecret: Buffer;
  private readonly epochMs: number;
  private readonly cellSizeDeg: number;
  private readonly authorityPublicKeyPem: string;
  private readonly gridSalt: Buffer;
  private readonly records: StoredRecord[] = [];
  readonly auditLog: AuditLog;

  constructor(config: VaultConfig) {
    this.masterSecret = config.masterSecret ?? randomKey();
    this.epochMs = config.epochMs ?? DEFAULT_EPOCH_MS;
    this.cellSizeDeg = config.cellSizeDeg ?? DEFAULT_CELL_SIZE_DEG;
    this.authorityPublicKeyPem = config.authorityPublicKeyPem;
    this.gridSalt = hkdf(this.masterSecret, 'tracevault-grid-salt');
    this.auditLog = new AuditLog(config.auditLogFile);
  }

  private epochOf(timestamp: number): number {
    return Math.floor(timestamp / this.epochMs);
  }

  private epochKey(epoch: number): Buffer {
    return hkdf(this.masterSecret, `tracevault-pseudonym-epoch:${epoch}`);
  }

  pseudonymFor(realId: string, timestamp: number): string {
    const epoch = this.epochOf(timestamp);
    return hmacSha256(this.epochKey(epoch), `pseudonym:${realId}`).toString('hex');
  }

  ingest(realId: string, lat: number, lng: number, timestamp: number): StoredRecord {
    const cell = gridCell(lat, lng, this.cellSizeDeg);
    const sealed: SealedPayload = { realId, lat, lng };
    const record: StoredRecord = {
      pseudonym: this.pseudonymFor(realId, timestamp),
      epoch: this.epochOf(timestamp),
      timestamp,
      gridHash: gridHash(this.gridSalt, cell),
      payload: eciesEncrypt(this.authorityPublicKeyPem, Buffer.from(JSON.stringify(sealed), 'utf8')),
    };
    this.records.push(record);
    return record;
  }

  recordCount(): number {
    return this.records.length;
  }

  heatmap(from: number, to: number): HeatCell[] {
    const counts = new Map<string, number>();
    for (const record of this.records) {
      if (record.timestamp < from || record.timestamp > to) continue;
      counts.set(record.gridHash, (counts.get(record.gridHash) ?? 0) + 1);
    }
    return [...counts.entries()]
      .map(([hash, count]) => ({ gridHash: hash, count }))
      .sort((a, b) => b.count - a.count);
  }

  reveal(credential: AuditCredential, revealKeyPem: string): TrajectoryPoint[] {
    try {
      verifyCredential(credential, this.authorityPublicKeyPem);
    } catch (err) {
      this.auditLog.append('AUDIT_DENIED', {
        credentialId: credential?.credentialId ?? null,
        reason: (err as Error).message,
      });
      throw err;
    }

    const targetPseudonyms = new Set<string>();
    const firstEpoch = this.epochOf(credential.from);
    const lastEpoch = this.epochOf(credential.to);
    for (let epoch = firstEpoch; epoch <= lastEpoch; epoch++) {
      targetPseudonyms.add(
        hmacSha256(this.epochKey(epoch), `pseudonym:${credential.targetRealId}`).toString('hex'),
      );
    }

    const matched = this.records.filter(
      (record) =>
        targetPseudonyms.has(record.pseudonym) &&
        record.timestamp >= credential.from &&
        record.timestamp <= credential.to,
    );

    const points: TrajectoryPoint[] = [];
    for (const record of matched) {
      const sealed = JSON.parse(
        eciesDecrypt(revealKeyPem, record.payload).toString('utf8'),
      ) as SealedPayload;
      if (sealed.realId !== credential.targetRealId) {
        this.auditLog.append('AUDIT_INTEGRITY_FAILURE', {
          credentialId: credential.credentialId,
          timestamp: record.timestamp,
        });
        throw new Error('decrypted payload does not match credential target');
      }
      points.push({
        timestamp: record.timestamp,
        realId: sealed.realId,
        lat: sealed.lat,
        lng: sealed.lng,
      });
    }
    points.sort((a, b) => a.timestamp - b.timestamp);

    this.auditLog.append('AUDIT_REVEAL', {
      credentialId: credential.credentialId,
      issuer: credential.issuer,
      purpose: credential.purpose,
      targetPseudonyms: [...targetPseudonyms],
      from: credential.from,
      to: credential.to,
      revealedPoints: points.length,
    });
    return points;
  }
}
