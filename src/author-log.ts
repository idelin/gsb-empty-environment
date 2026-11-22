import type { Checkpoint, MediationCert, RevocationCert, RotationCert } from './certs.ts';
import { verifyMediation } from './certs.ts';
import { ZERO_DIGEST } from './encoding.ts';
import type { SignedRecord } from './records.ts';

export type MsgStatus = 'certain' | 'quarantined' | 'unknown';

export interface HeadInfo {
  digest: string;
  seq: bigint;
  hasContent: boolean;
}

export interface ForkInfo {
  seq: bigint;
  digests: string[];
}

export interface PrefixEntry {
  seq: bigint;
  digest: string | null;
  hasContent: boolean;
  status: MsgStatus;
}

export interface GapInfo {
  kind: 'missing-record' | 'missing-content';
  digest: string;
  seq: bigint | null;
}

export interface QuarantinedRecord {
  digest: string;
  seq: bigint;
  reason: 'fork-conflict' | 'revoked-key' | 'rotation-scope';
}

export interface MediationState {
  applied: boolean;
  chooses: string | null;
  staleReason: string | null;
}

export interface AuthorView {
  author: string;
  heads: HeadInfo[];
  forks: ForkInfo[];
  commonPrefix: PrefixEntry[];
  unknownPrefixBelow: bigint;
  gaps: GapInfo[];
  quarantined: QuarantinedRecord[];
  lifted: string[];
  mediation: MediationState;
  readable: SignedRecord[];
}

export interface LogContext {
  mediatorKeys: Uint8Array[];
}

export class AuthorLog {
  readonly author: string;
  private readonly ctx: LogContext;
  readonly keys = new Map<number, Uint8Array>();
  readonly revokedGens = new Set<number>();
  readonly records = new Map<string, SignedRecord>();
  readonly checkpoints = new Map<string, bigint>();
  readonly rotations: RotationCert[] = [];
  readonly revocations: RevocationCert[] = [];
  readonly mediations: MediationCert[] = [];
  readonly pending: SignedRecord[] = [];
  readonly anomalies: string[] = [];

  constructor(author: string, ctx: LogContext) {
    this.author = author;
    this.ctx = ctx;
  }

  computeHeads(): HeadInfo[] {
    const hasChild = new Set<string>();
    for (const rec of this.records.values()) hasChild.add(rec.data.prev);
    const heads: HeadInfo[] = [];
    for (const rec of this.records.values()) {
      if (!hasChild.has(rec.digest)) {
        heads.push({ digest: rec.digest, seq: rec.data.seq, hasContent: true });
      }
    }
    for (const [digest, seq] of this.checkpoints) {
      if (!hasChild.has(digest) && !this.records.has(digest)) {
        heads.push({ digest, seq, hasContent: false });
      }
    }
    heads.sort((a, b) => (a.seq < b.seq ? -1 : a.seq > b.seq ? 1 : a.digest < b.digest ? -1 : 1));
    return heads;
  }

  computeForks(): ForkInfo[] {
    const forks: ForkInfo[] = [];
    const bySeqPrev = new Map<string, Set<string>>();
    const seqOf = new Map<string, bigint>();
    for (const rec of this.records.values()) {
      const key = `${rec.data.seq}:${rec.data.prev}`;
      let s = bySeqPrev.get(key);
      if (!s) bySeqPrev.set(key, (s = new Set()));
      s.add(rec.digest);
      seqOf.set(key, rec.data.seq);
    }
    for (const [key, digests] of bySeqPrev) {
      if (digests.size > 1) forks.push({ seq: seqOf.get(key)!, digests: [...digests].sort() });
    }
    const recordDigestAtSeq = new Map<bigint, Set<string>>();
    for (const rec of this.records.values()) {
      let s = recordDigestAtSeq.get(rec.data.seq);
      if (!s) recordDigestAtSeq.set(rec.data.seq, (s = new Set()));
      s.add(rec.digest);
    }
    for (const [digest, seq] of this.checkpoints) {
      const held = recordDigestAtSeq.get(seq);
      if (held && !held.has(digest)) {
        const existing = forks.find((f) => f.seq === seq);
        const digests = new Set(existing?.digests ?? []);
        digests.add(digest);
        for (const d of held) digests.add(d);
        if (existing) existing.digests = [...digests].sort();
        else forks.push({ seq, digests: [...digests].sort() });
      }
    }
    forks.sort((a, b) => (a.seq < b.seq ? -1 : 1));
    return forks;
  }

  private chainMap(headDigest: string): Map<bigint, string> {
    const map = new Map<bigint, string>();
    let d = headDigest;
    while (d !== ZERO_DIGEST) {
      const rec = this.records.get(d);
      if (rec) {
        map.set(rec.data.seq, d);
        d = rec.data.prev;
      } else {
        const cpSeq = this.checkpoints.get(d);
        if (cpSeq === undefined) break;
        map.set(cpSeq, d);
        break;
      }
    }
    return map;
  }

  private ancestryDigests(digest: string): Set<string> {
    return new Set(this.chainMap(digest).values());
  }

  rotationChainValid(rec: SignedRecord): boolean {
    const needed = rec.data.keyGen;
    const anchors: string[] = [];
    for (let gen = 0; gen < needed; gen++) {
      if (this.revokedGens.has(gen)) return false;
      const cert = this.rotations.find((r) => r.fromGen === gen && r.toGen === gen + 1);
      if (!cert) return false;
      anchors.push(cert.anchor);
    }
    if (this.revokedGens.has(needed)) return false;
    if (anchors.length === 0) return true;
    const anc = this.ancestryDigests(rec.digest);
    return anchors.every((a) => anc.has(a));
  }

  private computePrefix(heads: HeadInfo[]): {
    tipSeq: bigint | null;
    tipDigest: string | null;
    entries: PrefixEntry[];
    unknownBelow: bigint;
  } {
    if (heads.length === 0) return { tipSeq: null, tipDigest: null, entries: [], unknownBelow: 0n };
    const maps = heads.map((h) => this.chainMap(h.digest));
    let tipSeq: bigint | null = null;
    let tipDigest: string | null = null;
    for (const [seq, digest] of maps[0]) {
      if (maps.every((m) => m.get(seq) === digest) && (tipSeq === null || seq > tipSeq)) {
        tipSeq = seq;
        tipDigest = digest;
      }
    }
    if (tipSeq === null || tipDigest === null) return { tipSeq: null, tipDigest: null, entries: [], unknownBelow: 0n };
    const entries: PrefixEntry[] = [];
    let d: string = tipDigest;
    while (d !== ZERO_DIGEST) {
      const rec = this.records.get(d);
      if (rec) {
        entries.push({
          seq: rec.data.seq,
          digest: d,
          hasContent: true,
          status: this.rotationChainValid(rec) ? 'certain' : 'quarantined',
        });
        const prev = rec.data.prev;
        if (prev !== ZERO_DIGEST && !this.records.has(prev) && !this.checkpoints.has(prev)) {
          if (rec.data.seq > 0n) {
            entries.push({ seq: rec.data.seq - 1n, digest: prev, hasContent: false, status: 'unknown' });
          }
          break;
        }
        d = rec.data.prev;
      } else {
        const cpSeq = this.checkpoints.get(d);
        if (cpSeq === undefined) break;
        entries.push({ seq: cpSeq, digest: d, hasContent: false, status: 'unknown' });
        break;
      }
    }
    entries.reverse();
    const lowestKnown = entries.length > 0 ? entries[0].seq : tipSeq + 1n;
    return { tipSeq, tipDigest, entries, unknownBelow: lowestKnown };
  }

  private computeMediation(heads: HeadInfo[]): MediationState {
    const valid = this.mediations.filter((m) => verifyMediation(m, this.ctx.mediatorKeys));
    for (const cert of valid) {
      const named = new Set(cert.namesHeads);
      if (!named.has(cert.chooses)) {
        return { applied: false, chooses: null, staleReason: 'cert-chooses-unlisted-head' };
      }
      const stale = heads.find((h) => h.seq <= cert.observedUpToSeq && !named.has(h.digest));
      if (stale) {
        return { applied: false, chooses: null, staleReason: `unnamed-head:${stale.digest}` };
      }
      return { applied: true, chooses: cert.chooses, staleReason: null };
    }
    return { applied: false, chooses: null, staleReason: null };
  }

  private quarantineReason(rec: SignedRecord): QuarantinedRecord['reason'] {
    if (this.revokedGens.has(rec.data.keyGen)) return 'revoked-key';
    if (!this.rotationChainValid(rec)) return 'rotation-scope';
    return 'fork-conflict';
  }

  computeGaps(): GapInfo[] {
    const gaps: GapInfo[] = [];
    const seen = new Set<string>();
    for (const rec of this.records.values()) {
      const prev = rec.data.prev;
      if (prev !== ZERO_DIGEST && !this.records.has(prev) && !this.checkpoints.has(prev) && !seen.has(prev)) {
        seen.add(prev);
        gaps.push({ kind: 'missing-record', digest: prev, seq: rec.data.seq - 1n });
      }
    }
    for (const [digest, seq] of this.checkpoints) {
      if (!this.records.has(digest)) gaps.push({ kind: 'missing-content', digest, seq });
    }
    return gaps;
  }

  view(): AuthorView {
    const heads = this.computeHeads();
    const forks = this.computeForks();
    const { entries, unknownBelow } = this.computePrefix(heads);
    const mediation = this.computeMediation(heads);
    const prefixDigests = new Set(entries.map((e) => e.digest).filter((d): d is string => d !== null));
    const lifted = new Set<string>();
    if (mediation.applied && mediation.chooses !== null) {
      let d: string | null = mediation.chooses;
      while (d !== null && d !== ZERO_DIGEST && !prefixDigests.has(d)) {
        const rec = this.records.get(d);
        if (!rec || !this.rotationChainValid(rec)) break;
        lifted.add(d);
        d = rec.data.prev;
      }
    }
    const quarantined: QuarantinedRecord[] = [];
    const entryStatus = new Map(entries.map((e) => [e.digest, e.status]));
    for (const rec of this.records.values()) {
      if (lifted.has(rec.digest)) continue;
      if (prefixDigests.has(rec.digest) && entryStatus.get(rec.digest) === 'certain') continue;
      quarantined.push({ digest: rec.digest, seq: rec.data.seq, reason: this.quarantineReason(rec) });
    }
    quarantined.sort((a, b) => (a.seq < b.seq ? -1 : a.seq > b.seq ? 1 : 0));
    const readable: SignedRecord[] = [];
    for (const e of entries) {
      if (e.status === 'certain' && e.hasContent && e.digest !== null) {
        readable.push(this.records.get(e.digest)!);
      }
    }
    const liftedRecs = [...lifted]
      .map((d) => this.records.get(d)!)
      .sort((a, b) => (a.data.seq < b.data.seq ? -1 : 1));
    readable.push(...liftedRecs);
    return {
      author: this.author,
      heads,
      forks,
      commonPrefix: entries,
      unknownPrefixBelow: unknownBelow,
      gaps: this.computeGaps(),
      quarantined,
      lifted: [...lifted],
      mediation,
      readable,
    };
  }
}
