import { concat, fromHex, lenPrefixed, sha256, u32be, u64be, utf8, ZERO_DIGEST } from './encoding.ts';
import { signBytes, type KeyPair } from './keys.ts';

export interface RecordData {
  author: string;
  keyGen: number;
  seq: bigint;
  prev: string;
  body: Uint8Array;
}

export interface SignedRecord {
  data: RecordData;
  digest: string;
  signature: Uint8Array;
  signerKey: Uint8Array;
}

export function encodeRecordData(d: RecordData): Uint8Array {
  return concat([
    lenPrefixed(utf8(d.author)),
    u32be(d.keyGen),
    u64be(d.seq),
    fromHex(d.prev),
    lenPrefixed(d.body),
  ]);
}

export function digestOf(d: RecordData): string {
  return sha256(encodeRecordData(d));
}

export function makeRecord(kp: KeyPair, data: RecordData): SignedRecord {
  const digest = digestOf(data);
  return {
    data,
    digest,
    signature: signBytes(kp.privateDer, fromHex(digest)),
    signerKey: kp.publicDer,
  };
}

export function genesisData(author: string, keyGen: number, body: Uint8Array): RecordData {
  return { author, keyGen, seq: 0n, prev: ZERO_DIGEST, body };
}
