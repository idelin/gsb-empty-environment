import { AuthorLog, type AuthorView } from './author-log.ts';
import {
  verifyCheckpoint,
  verifyMediation,
  verifyRevocation,
  verifyRotation,
  type Checkpoint,
  type MediationCert,
  type RevocationCert,
  type RotationCert,
} from './certs.ts';
import { fromHex, toHex } from './encoding.ts';
import { verifyBytes } from './keys.ts';
import { digestOf, type SignedRecord } from './records.ts';

export interface HostConfig {
  authors: { id: string; gen0Key: Uint8Array }[];
  relayKeys?: Uint8Array[];
  mediatorKeys?: Uint8Array[];
  witnessKeys?: Uint8Array[];
  witnessThreshold?: number;
}

export interface IngestResult {
  accepted: boolean;
  reason: string;
}

interface SerializedRecord {
  author: string;
  keyGen: number;
  seq: string;
  prev: string;
  body: string;
  digest: string;
  signature: string;
  signerKey: string;
}

interface SerializedLog {
  id: string;
  keys: { gen: number; key: string }[];
  revokedGens: number[];
  records: SerializedRecord[];
  checkpoints: { digest: string; seq: string }[];
  rotations: { author: string; fromGen: number; toGen: number; newKey: string; anchor: string; oldSig: string; newSig: string }[];
  revocations: { author: string; gen: number; witnesses: { key: string; sig: string }[] }[];
  mediations: { author: string; chooses: string; namesHeads: string[]; observedUpToSeq: string; mediator: string; sig: string }[];
  pending: SerializedRecord[];
  anomalies: string[];
}

export interface Snapshot {
  format: 1;
  authors: SerializedLog[];
}

function serializeRecord(rec: SignedRecord): SerializedRecord {
  return {
    author: rec.data.author,
    keyGen: rec.data.keyGen,
    seq: rec.data.seq.toString(),
    prev: rec.data.prev,
    body: toHex(rec.data.body),
    digest: rec.digest,
    signature: toHex(rec.signature),
    signerKey: toHex(rec.signerKey),
  };
}

function deserializeRecord(s: SerializedRecord): SignedRecord {
  return {
    data: { author: s.author, keyGen: s.keyGen, seq: BigInt(s.seq), prev: s.prev, body: fromHex(s.body) },
    digest: s.digest,
    signature: fromHex(s.signature),
    signerKey: fromHex(s.signerKey),
  };
}

function serializeLog(log: AuthorLog): SerializedLog {
  return {
    id: log.author,
    keys: [...log.keys].map(([gen, key]) => ({ gen, key: toHex(key) })),
    revokedGens: [...log.revokedGens],
    records: [...log.records.values()].map(serializeRecord),
    checkpoints: [...log.checkpoints].map(([digest, seq]) => ({ digest, seq: seq.toString() })),
    rotations: log.rotations.map((r) => ({
      author: r.author,
      fromGen: r.fromGen,
      toGen: r.toGen,
      newKey: toHex(r.newKey),
      anchor: r.anchor,
      oldSig: toHex(r.oldSig),
      newSig: toHex(r.newSig),
    })),
    revocations: log.revocations.map((r) => ({
      author: r.author,
      gen: r.gen,
      witnesses: r.witnesses.map((w) => ({ key: toHex(w.key), sig: toHex(w.sig) })),
    })),
    mediations: log.mediations.map((m) => ({
      author: m.author,
      chooses: m.chooses,
      namesHeads: m.namesHeads,
      observedUpToSeq: m.observedUpToSeq.toString(),
      mediator: toHex(m.mediator),
      sig: toHex(m.sig),
    })),
    pending: log.pending.map(serializeRecord),
    anomalies: [...log.anomalies],
  };
}

function restoreLog(s: SerializedLog, ctx: { mediatorKeys: Uint8Array[] }): AuthorLog {
  const log = new AuthorLog(s.id, ctx);
  for (const k of s.keys) log.keys.set(k.gen, fromHex(k.key));
  for (const g of s.revokedGens) log.revokedGens.add(g);
  for (const r of s.records) {
    const rec = deserializeRecord(r);
    log.records.set(rec.digest, rec);
  }
  for (const c of s.checkpoints) log.checkpoints.set(c.digest, BigInt(c.seq));
  for (const r of s.rotations) {
    log.rotations.push({
      author: r.author,
      fromGen: r.fromGen,
      toGen: r.toGen,
      newKey: fromHex(r.newKey),
      anchor: r.anchor,
      oldSig: fromHex(r.oldSig),
      newSig: fromHex(r.newSig),
    });
  }
  for (const r of s.revocations) {
    log.revocations.push({
      author: r.author,
      gen: r.gen,
      witnesses: r.witnesses.map((w) => ({ key: fromHex(w.key), sig: fromHex(w.sig) })),
    });
  }
  for (const m of s.mediations) {
    log.mediations.push({
      author: m.author,
      chooses: m.chooses,
      namesHeads: m.namesHeads,
      observedUpToSeq: BigInt(m.observedUpToSeq),
      mediator: fromHex(m.mediator),
      sig: fromHex(m.sig),
    });
  }
  for (const p of s.pending) log.pending.push(deserializeRecord(p));
  log.anomalies.push(...s.anomalies);
  return log;
}

export class IngestHost {
  private readonly logs = new Map<string, AuthorLog>();
  private readonly gen0Keys = new Map<string, Uint8Array>();
  private readonly relayKeys: Uint8Array[];
  private readonly mediatorKeys: Uint8Array[];
  private readonly witnessKeys: Uint8Array[];
  private readonly witnessThreshold: number;

  constructor(config: HostConfig) {
    for (const a of config.authors) this.gen0Keys.set(a.id, a.gen0Key);
    this.relayKeys = config.relayKeys ?? [];
    this.mediatorKeys = config.mediatorKeys ?? [];
    this.witnessKeys = config.witnessKeys ?? [];
    this.witnessThreshold = config.witnessThreshold ?? 1;
  }

  private logFor(author: string): AuthorLog | null {
    let log = this.logs.get(author);
    if (!log) {
      const gen0 = this.gen0Keys.get(author);
      if (!gen0) return null;
      log = new AuthorLog(author, { mediatorKeys: this.mediatorKeys });
      log.keys.set(0, gen0);
      this.logs.set(author, log);
    }
    return log;
  }

  ingestRecord(rec: SignedRecord): IngestResult {
    const log = this.logFor(rec.data.author);
    if (!log) return { accepted: false, reason: 'unknown-author' };
    if (rec.digest !== digestOf(rec.data)) return { accepted: false, reason: 'digest-mismatch' };
    if (log.records.has(rec.digest)) return { accepted: true, reason: 'duplicate' };
    const key = log.keys.get(rec.data.keyGen);
    if (!key) {
      if (!log.pending.some((p) => p.digest === rec.digest)) log.pending.push(rec);
      return { accepted: false, reason: 'key-unknown-pending' };
    }
    if (toHex(rec.signerKey) !== toHex(key)) return { accepted: false, reason: 'signer-key-mismatch' };
    if (!verifyBytes(key, fromHex(rec.digest), rec.signature)) return { accepted: false, reason: 'bad-signature' };
    log.records.set(rec.digest, rec);
    return { accepted: true, reason: 'ok' };
  }

  ingestCheckpoint(cp: Checkpoint): IngestResult {
    const log = this.logFor(cp.author);
    if (!log) return { accepted: false, reason: 'unknown-author' };
    if (!verifyCheckpoint(cp, this.relayKeys)) return { accepted: false, reason: 'bad-relay-signature' };
    const existing = log.records.get(cp.digest);
    if (existing && existing.data.seq !== cp.seq) {
      log.anomalies.push(`checkpoint-seq-conflict:${cp.digest}`);
      return { accepted: false, reason: 'checkpoint-seq-conflict' };
    }
    log.checkpoints.set(cp.digest, cp.seq);
    return { accepted: true, reason: 'ok' };
  }

  ingestRotation(cert: RotationCert): IngestResult {
    const log = this.logFor(cert.author);
    if (!log) return { accepted: false, reason: 'unknown-author' };
    if (cert.toGen !== cert.fromGen + 1) return { accepted: false, reason: 'non-adjacent-generation' };
    const oldKey = log.keys.get(cert.fromGen);
    if (!oldKey) return { accepted: false, reason: 'unknown-source-generation' };
    if (!verifyRotation(cert, oldKey)) return { accepted: false, reason: 'bad-rotation-signature' };
    if (!log.rotations.some((r) => r.fromGen === cert.fromGen && r.toGen === cert.toGen)) {
      log.rotations.push(cert);
    }
    log.keys.set(cert.toGen, cert.newKey);
    this.retryPending(log);
    return { accepted: true, reason: 'ok' };
  }

  ingestRevocation(cert: RevocationCert): IngestResult {
    const log = this.logFor(cert.author);
    if (!log) return { accepted: false, reason: 'unknown-author' };
    if (!verifyRevocation(cert, this.witnessKeys, this.witnessThreshold)) {
      return { accepted: false, reason: 'witness-threshold-not-met' };
    }
    if (!log.revocations.some((r) => r.gen === cert.gen)) log.revocations.push(cert);
    log.revokedGens.add(cert.gen);
    return { accepted: true, reason: 'ok' };
  }

  ingestMediation(cert: MediationCert): IngestResult {
    const log = this.logFor(cert.author);
    if (!log) return { accepted: false, reason: 'unknown-author' };
    if (!verifyMediation(cert, this.mediatorKeys)) return { accepted: false, reason: 'bad-mediator-signature' };
    if (!log.mediations.some((m) => m.sig.every((b, i) => b === cert.sig[i]) && m.chooses === cert.chooses)) {
      log.mediations.push(cert);
    }
    return { accepted: true, reason: 'ok' };
  }

  private retryPending(log: AuthorLog): void {
    let progress = true;
    while (progress) {
      progress = false;
      for (let i = log.pending.length - 1; i >= 0; i--) {
        const rec = log.pending[i];
        if (log.records.has(rec.digest)) {
          log.pending.splice(i, 1);
          continue;
        }
        const key = log.keys.get(rec.data.keyGen);
        if (!key) continue;
        if (toHex(rec.signerKey) === toHex(key) && verifyBytes(key, fromHex(rec.digest), rec.signature)) {
          log.records.set(rec.digest, rec);
          log.pending.splice(i, 1);
          progress = true;
        }
      }
    }
  }

  view(author: string): AuthorView {
    const log = this.logs.get(author);
    if (!log) {
      return {
        author,
        heads: [],
        forks: [],
        commonPrefix: [],
        unknownPrefixBelow: 0n,
        gaps: [],
        quarantined: [],
        lifted: [],
        mediation: { applied: false, chooses: null, staleReason: null },
        readable: [],
      };
    }
    return log.view();
  }

  snapshot(): Snapshot {
    return { format: 1, authors: [...this.logs.values()].map(serializeLog) };
  }

  static restore(snapshot: Snapshot, config: HostConfig): IngestHost {
    const host = new IngestHost(config);
    for (const s of snapshot.authors) {
      host.logs.set(s.id, restoreLog(s, { mediatorKeys: host.mediatorKeys }));
    }
    return host;
  }

  mergeSnapshot(snapshot: Snapshot): void {
    for (const s of snapshot.authors) {
      const incoming = restoreLog(s, { mediatorKeys: this.mediatorKeys });
      const log = this.logFor(s.id);
      if (!log) continue;
      for (const [gen, key] of incoming.keys) if (!log.keys.has(gen)) log.keys.set(gen, key);
      for (const g of incoming.revokedGens) log.revokedGens.add(g);
      for (const [digest, rec] of incoming.records) if (!log.records.has(digest)) log.records.set(digest, rec);
      for (const [digest, seq] of incoming.checkpoints) if (!log.checkpoints.has(digest)) log.checkpoints.set(digest, seq);
      for (const r of incoming.rotations) {
        if (!log.rotations.some((x) => x.fromGen === r.fromGen && x.toGen === r.toGen)) log.rotations.push(r);
      }
      for (const r of incoming.revocations) {
        if (!log.revocations.some((x) => x.gen === r.gen)) log.revocations.push(r);
      }
      for (const m of incoming.mediations) {
        if (!log.mediations.some((x) => x.chooses === m.chooses && x.observedUpToSeq === m.observedUpToSeq)) {
          log.mediations.push(m);
        }
      }
      for (const rec of incoming.pending) {
        if (!log.records.has(rec.digest) && !log.pending.some((p) => p.digest === rec.digest)) {
          log.pending.push(rec);
        }
      }
      this.retryPending(log);
    }
  }
}
