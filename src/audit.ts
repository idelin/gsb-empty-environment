import { randomBytes } from "node:crypto";
import { hmacHex, safeEqualHex, sha256Hex } from "./crypto.ts";
import type { TrajectoryIndex, TrajectoryPoint } from "./store.ts";

export interface AuditCredential {
  caseId: string;
  entityId: string;
  from: number;
  to: number;
  expiresAt: number;
  issuer: string;
  signature: string;
}

export interface AuditLogEntry {
  seq: number;
  timestamp: number;
  action: "TARGETED_REVEAL" | "REVEAL_DENIED";
  caseId: string;
  entityId: string;
  from: number;
  to: number;
  resultCount: number;
  reason?: string;
  prevHash: string;
  hash: string;
}

function canonicalCredential(c: Omit<AuditCredential, "signature">): string {
  return [c.caseId, c.entityId, c.from, c.to, c.expiresAt, c.issuer].join("|");
}

export class AuditAuthority {
  private readonly authorityKey: Buffer;
  readonly issuerId: string;

  constructor(authorityKey: Buffer, issuerId: string) {
    this.authorityKey = authorityKey;
    this.issuerId = issuerId;
  }

  static generate(issuerId: string): AuditAuthority {
    return new AuditAuthority(randomBytes(32), issuerId);
  }

  issue(
    caseId: string,
    entityId: string,
    from: number,
    to: number,
    ttlMs: number,
    now: number = Date.now(),
  ): AuditCredential {
    if (from > to) throw new RangeError("credential time range is inverted");
    const base = {
      caseId,
      entityId,
      from,
      to,
      expiresAt: now + ttlMs,
      issuer: this.issuerId,
    };
    return { ...base, signature: hmacHex(this.authorityKey, canonicalCredential(base)) };
  }

  verify(credential: AuditCredential, now: number = Date.now()): void {
    const { signature, ...base } = credential;
    if (base.issuer !== this.issuerId) {
      throw new Error("credential issuer is not recognized");
    }
    const expected = hmacHex(this.authorityKey, canonicalCredential(base));
    if (!safeEqualHex(expected, signature)) {
      throw new Error("credential signature is invalid");
    }
    if (now > credential.expiresAt) {
      throw new Error("credential has expired");
    }
  }
}

const GENESIS_HASH = "0".repeat(64);

export class AuditLog {
  private readonly entries: AuditLogEntry[] = [];

  append(
    entry: Omit<AuditLogEntry, "seq" | "timestamp" | "prevHash" | "hash">,
    now: number = Date.now(),
  ): AuditLogEntry {
    const prevHash =
      this.entries.length === 0
        ? GENESIS_HASH
        : this.entries[this.entries.length - 1].hash;
    const partial = {
      seq: this.entries.length,
      timestamp: now,
      ...entry,
      prevHash,
    };
    const hash = sha256Hex(JSON.stringify(partial));
    const full: AuditLogEntry = { ...partial, hash };
    this.entries.push(full);
    return full;
  }

  list(): readonly AuditLogEntry[] {
    return this.entries;
  }

  verify(): boolean {
    let prevHash = GENESIS_HASH;
    for (let i = 0; i < this.entries.length; i++) {
      const entry = this.entries[i];
      if (entry.seq !== i || entry.prevHash !== prevHash) return false;
      const { hash, ...rest } = entry;
      if (sha256Hex(JSON.stringify(rest)) !== hash) return false;
      prevHash = hash;
    }
    return true;
  }
}

export class Auditor {
  private readonly authority: AuditAuthority;
  private readonly index: TrajectoryIndex;
  private readonly log: AuditLog;

  constructor(authority: AuditAuthority, index: TrajectoryIndex, log: AuditLog) {
    this.authority = authority;
    this.index = index;
    this.log = log;
  }

  reveal(credential: AuditCredential, now: number = Date.now()): TrajectoryPoint[] {
    try {
      this.authority.verify(credential, now);
    } catch (err) {
      this.log.append(
        {
          action: "REVEAL_DENIED",
          caseId: credential.caseId,
          entityId: credential.entityId,
          from: credential.from,
          to: credential.to,
          resultCount: 0,
          reason: err instanceof Error ? err.message : String(err),
        },
        now,
      );
      throw err;
    }
    const points = this.index.reveal(credential.entityId, credential.from, credential.to);
    this.log.append(
      {
        action: "TARGETED_REVEAL",
        caseId: credential.caseId,
        entityId: credential.entityId,
        from: credential.from,
        to: credential.to,
        resultCount: points.length,
      },
      now,
    );
    return points;
  }
}
