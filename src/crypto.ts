import {
  createCipheriv,
  createDecipheriv,
  createHash,
  createHmac,
  hkdfSync,
  randomBytes,
  timingSafeEqual,
} from "node:crypto";

export type MasterKey = Buffer;

export interface KeyHierarchy {
  pseudonymKey: Buffer;
  gridSalt: Buffer;
  recordKey: Buffer;
}

export interface SealedRecord {
  iv: string;
  tag: string;
  data: string;
}

export function generateMasterKey(): MasterKey {
  return randomBytes(32);
}

export function deriveKeys(master: MasterKey): KeyHierarchy {
  const derive = (info: string): Buffer =>
    Buffer.from(hkdfSync("sha256", master, "trace-veil", info, 32));
  return {
    pseudonymKey: derive("pseudonym-v1"),
    gridSalt: derive("grid-salt-v1"),
    recordKey: derive("record-enc-v1"),
  };
}

export function pseudonymFor(
  pseudonymKey: Buffer,
  entityId: string,
  bucket: number,
): string {
  return createHmac("sha256", pseudonymKey)
    .update(`entity:${entityId}|bucket:${bucket}`)
    .digest("hex")
    .slice(0, 32);
}

export function cellIndexKey(
  gridSalt: Buffer,
  cellId: string,
  bucket: number,
): string {
  return createHmac("sha256", gridSalt)
    .update(`cell:${cellId}|bucket:${bucket}`)
    .digest("hex")
    .slice(0, 32);
}

export function seal(
  recordKey: Buffer,
  plaintext: string,
  aad: string,
): SealedRecord {
  const iv = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", recordKey, iv);
  cipher.setAAD(Buffer.from(aad, "utf8"));
  const data = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final()]);
  return {
    iv: iv.toString("base64"),
    tag: cipher.getAuthTag().toString("base64"),
    data: data.toString("base64"),
  };
}

export function unseal(
  recordKey: Buffer,
  sealed: SealedRecord,
  aad: string,
): string {
  const decipher = createDecipheriv(
    "aes-256-gcm",
    recordKey,
    Buffer.from(sealed.iv, "base64"),
  );
  decipher.setAAD(Buffer.from(aad, "utf8"));
  decipher.setAuthTag(Buffer.from(sealed.tag, "base64"));
  return Buffer.concat([
    decipher.update(Buffer.from(sealed.data, "base64")),
    decipher.final(),
  ]).toString("utf8");
}

export function sha256Hex(input: string): string {
  return createHash("sha256").update(input, "utf8").digest("hex");
}

export function hmacHex(key: Buffer, input: string): string {
  return createHmac("sha256", key).update(input, "utf8").digest("hex");
}

export function safeEqualHex(a: string, b: string): boolean {
  const ba = Buffer.from(a, "hex");
  const bb = Buffer.from(b, "hex");
  return ba.length === bb.length && timingSafeEqual(ba, bb);
}
