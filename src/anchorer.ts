import {
  createHash,
  generateKeyPairSync,
  randomBytes,
  sign,
  verify as cryptoVerify,
  type KeyObject,
} from 'node:crypto';

export interface AnchorRecord {
  version: 1;
  sequence: number;        // strictly increasing per anchorer
  timestamp: number;       // strictly increasing per anchorer
  merkleRoot: string;      // hex
  prevAnchorHash: string;  // hash chain over previous anchor payload; genesis = 64 zeros
  nonce: string;           // random per anchor, uniqueness / domain separation
}

export interface SignedAnchor {
  record: AnchorRecord;
  signature: string;  // hex, Ed25519 over canonicalPayload(record)
  publicKey: string;  // PEM of the anchorer's Ed25519 public key
}

export function anchorHash(record: AnchorRecord): string {
  return createHash('sha256').update(canonicalPayload(record)).digest('hex');
}

export function canonicalPayload(record: AnchorRecord): Buffer {
  return Buffer.from(
    [
      `v=${record.version}`,
      `seq=${record.sequence}`,
      `ts=${record.timestamp}`,
      `root=${record.merkleRoot}`,
      `prev=${record.prevAnchorHash}`,
      `nonce=${record.nonce}`,
    ].join('|'),
    'utf8',
  );
}

const GENESIS_HASH = '0'.repeat(64);

export class Anchorer {
  private readonly privateKey: KeyObject;
  readonly publicKeyPem: string;
  private readonly timeSource: () => number;
  private sequence = 0;
  private lastTimestamp = -Infinity;
  private lastAnchorHash = GENESIS_HASH;

  constructor(
    timeSource: () => number,
    keys?: { publicKey: KeyObject; privateKey: KeyObject },
  ) {
    this.timeSource = timeSource;
    const pair = keys ?? generateKeyPairSync('ed25519');
    this.privateKey = pair.privateKey;
    this.publicKeyPem = (pair.publicKey.export({ format: 'pem', type: 'spki' }) as Buffer).toString();
  }

  /** Anchor the given Merkle root at the current time, returning a signed, chained record. */
  anchor(merkleRoot: Buffer): SignedAnchor {
    const timestamp = this.timeSource();
    if (!Number.isFinite(timestamp)) throw new Error('time source returned a non-finite timestamp');
    if (timestamp <= this.lastTimestamp) {
      throw new Error(`non-monotonic timestamp ${timestamp} (last was ${this.lastTimestamp})`);
    }
    const record: AnchorRecord = {
      version: 1,
      sequence: this.sequence,
      timestamp,
      merkleRoot: merkleRoot.toString('hex'),
      prevAnchorHash: this.lastAnchorHash,
      nonce: randomBytes(16).toString('hex'),
    };
    const signature = sign(null, canonicalPayload(record), this.privateKey).toString('hex');
    this.sequence += 1;
    this.lastTimestamp = timestamp;
    this.lastAnchorHash = anchorHash(record);
    return { record, signature, publicKey: this.publicKeyPem };
  }

  /** Stateless check: signature valid and record well-formed. */
  static verifySignature(anchor: SignedAnchor): boolean {
    const r = anchor.record;
    if (r.version !== 1 || !Number.isInteger(r.sequence) || r.sequence < 0) return false;
    if (!Number.isFinite(r.timestamp)) return false;
    if (!/^[0-9a-f]{64}$/.test(r.merkleRoot) || !/^[0-9a-f]{64}$/.test(r.prevAnchorHash)) return false;
    if (!/^[0-9a-f]{32}$/.test(r.nonce)) return false;
    return cryptoVerify(null, canonicalPayload(r), anchor.publicKey, Buffer.from(anchor.signature, 'hex'));
  }
}

/**
 * Stateful verifier tracking an anchorer's chain. Rejects replays, reordering,
 * timestamp regression and chain breaks — each anchor is accepted at most once.
 */
export class AnchorVerifier {
  private expectedSequence = 0;
  private expectedPrevHash = GENESIS_HASH;
  private lastTimestamp = -Infinity;
  private readonly seenNonces = new Set<string>();

  readonly trustedPublicKeyPem: string;

  constructor(trustedPublicKeyPem: string) {
    this.trustedPublicKeyPem = trustedPublicKeyPem;
  }

  accept(anchor: SignedAnchor): AnchorRecord {
    if (anchor.publicKey !== this.trustedPublicKeyPem) {
      throw new Error('untrusted anchor public key');
    }
    if (!Anchorer.verifySignature(anchor)) {
      throw new Error('invalid anchor signature');
    }
    const r = anchor.record;
    if (r.sequence !== this.expectedSequence) {
      throw new Error(`replay/reorder detected: expected sequence ${this.expectedSequence}, got ${r.sequence}`);
    }
    if (r.prevAnchorHash !== this.expectedPrevHash) {
      throw new Error('anchor chain break: prevAnchorHash mismatch');
    }
    if (r.timestamp <= this.lastTimestamp) {
      throw new Error('timestamp regression detected');
    }
    if (this.seenNonces.has(r.nonce)) {
      throw new Error('nonce reuse detected');
    }
    this.seenNonces.add(r.nonce);
    this.expectedSequence += 1;
    this.expectedPrevHash = anchorHash(r);
    this.lastTimestamp = r.timestamp;
    return r;
  }
}
