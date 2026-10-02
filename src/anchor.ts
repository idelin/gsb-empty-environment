import {
  createHash,
  generateKeyPairSync,
  randomBytes,
  sign as edSign,
  verify as edVerify,
  type KeyObject,
} from "node:crypto";

export interface AnchorRecord {
  version: 1;
  anchorId: string;
  seq: number;
  merkleRoot: string;
  timestamp: number;
  prevAnchorHash: string;
  signature: string;
}

export interface AnchorKeyPair {
  publicKey: KeyObject;
  privateKey: KeyObject;
}

export function generateAnchorKeys(): AnchorKeyPair {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  return { publicKey, privateKey };
}

export function canonicalPayload(
  record: Omit<AnchorRecord, "signature">,
): Buffer {
  return Buffer.from(
    [
      "ANCHOR",
      `v${record.version}`,
      record.anchorId,
      String(record.seq),
      String(record.timestamp),
      record.merkleRoot,
      record.prevAnchorHash,
    ].join("|"),
    "utf8",
  );
}

export function hashAnchor(record: AnchorRecord): string {
  const { signature, ...unsigned } = record;
  return createHash("sha256").update(canonicalPayload(unsigned)).digest("hex");
}

export type Clock = () => number;

export class AnchorService {
  readonly publicKey: KeyObject;
  private readonly privateKey: KeyObject;
  private readonly clock: Clock;
  private seq = 0;
  private prevAnchorHash = "0".repeat(64);

  constructor(keys: AnchorKeyPair, clock: Clock = () => Date.now()) {
    this.publicKey = keys.publicKey;
    this.privateKey = keys.privateKey;
    this.clock = clock;
  }

  anchor(merkleRoot: Buffer | string, timestamp?: number): AnchorRecord {
    const rootHex =
      typeof merkleRoot === "string" ? merkleRoot : merkleRoot.toString("hex");
    const unsigned: Omit<AnchorRecord, "signature"> = {
      version: 1,
      anchorId: randomBytes(16).toString("hex"),
      seq: this.seq,
      merkleRoot: rootHex,
      timestamp: timestamp ?? this.clock(),
      prevAnchorHash: this.prevAnchorHash,
    };
    const signature = edSign(
      null,
      canonicalPayload(unsigned),
      this.privateKey,
    ).toString("hex");
    const record: AnchorRecord = { ...unsigned, signature };
    this.seq += 1;
    this.prevAnchorHash = hashAnchor(record);
    return record;
  }
}

export function verifyAnchorSignature(
  record: AnchorRecord,
  publicKey: KeyObject,
): boolean {
  const { signature, ...unsigned } = record;
  try {
    return edVerify(
      null,
      canonicalPayload(unsigned),
      publicKey,
      Buffer.from(signature, "hex"),
    );
  } catch {
    return false;
  }
}

export interface LedgerCheckResult {
  ok: boolean;
  reason?: string;
}

export class AnchorLedger {
  private readonly seenIds = new Set<string>();
  private lastSeq = -1;
  private lastTimestamp = -Infinity;
  private lastHash = "0".repeat(64);

  register(record: AnchorRecord, publicKey: KeyObject): LedgerCheckResult {
    if (!verifyAnchorSignature(record, publicKey)) {
      return { ok: false, reason: "invalid anchor signature" };
    }
    if (this.seenIds.has(record.anchorId)) {
      return { ok: false, reason: "replay: anchorId already registered" };
    }
    if (record.seq <= this.lastSeq) {
      return {
        ok: false,
        reason: `replay: seq ${record.seq} not greater than last seq ${this.lastSeq}`,
      };
    }
    if (record.timestamp < this.lastTimestamp) {
      return {
        ok: false,
        reason: `replay: timestamp ${record.timestamp} before last timestamp ${this.lastTimestamp}`,
      };
    }
    if (record.prevAnchorHash !== this.lastHash) {
      return {
        ok: false,
        reason: "chain break: prevAnchorHash does not match last anchor hash",
      };
    }
    this.seenIds.add(record.anchorId);
    this.lastSeq = record.seq;
    this.lastTimestamp = record.timestamp;
    this.lastHash = hashAnchor(record);
    return { ok: true };
  }
}
