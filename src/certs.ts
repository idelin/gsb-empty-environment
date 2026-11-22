import { fromHex, lenPrefixed, taggedBytes, u32be, u64be, utf8 } from './encoding.ts';
import { keyIdOf, signBytes, verifyBytes, type KeyPair } from './keys.ts';

export interface RotationCert {
  author: string;
  fromGen: number;
  toGen: number;
  newKey: Uint8Array;
  anchor: string;
  oldSig: Uint8Array;
  newSig: Uint8Array;
}

export function rotationPayload(c: RotationCert): Uint8Array {
  return taggedBytes(
    'ROT1',
    lenPrefixed(utf8(c.author)),
    u32be(c.fromGen),
    u32be(c.toGen),
    lenPrefixed(c.newKey),
    fromHex(c.anchor),
  );
}

export function makeRotation(
  author: string,
  fromGen: number,
  toGen: number,
  newKp: KeyPair,
  anchor: string,
  oldKp: KeyPair,
): RotationCert {
  const cert: RotationCert = {
    author,
    fromGen,
    toGen,
    newKey: newKp.publicDer,
    anchor,
    oldSig: new Uint8Array(),
    newSig: new Uint8Array(),
  };
  const payload = rotationPayload(cert);
  cert.oldSig = signBytes(oldKp.privateDer, payload);
  cert.newSig = signBytes(newKp.privateDer, payload);
  return cert;
}

export function verifyRotation(cert: RotationCert, oldKey: Uint8Array): boolean {
  const payload = rotationPayload(cert);
  return verifyBytes(oldKey, payload, cert.oldSig) && verifyBytes(cert.newKey, payload, cert.newSig);
}

export interface RevocationCert {
  author: string;
  gen: number;
  witnesses: { key: Uint8Array; sig: Uint8Array }[];
}

export function revocationPayload(author: string, gen: number): Uint8Array {
  return taggedBytes('REV1', lenPrefixed(utf8(author)), u32be(gen));
}

export function makeRevocation(author: string, gen: number, witnessKps: KeyPair[]): RevocationCert {
  const payload = revocationPayload(author, gen);
  return {
    author,
    gen,
    witnesses: witnessKps.map((kp) => ({ key: kp.publicDer, sig: signBytes(kp.privateDer, payload) })),
  };
}

export function verifyRevocation(
  cert: RevocationCert,
  trustedWitnessKeys: Uint8Array[],
  threshold: number,
): boolean {
  const payload = revocationPayload(cert.author, cert.gen);
  const trusted = new Set(trustedWitnessKeys.map((k) => keyIdOf(k)));
  const seen = new Set<string>();
  for (const w of cert.witnesses) {
    const id = keyIdOf(w.key);
    if (!trusted.has(id) || seen.has(id)) continue;
    if (verifyBytes(w.key, payload, w.sig)) seen.add(id);
  }
  return seen.size >= threshold;
}

export interface Checkpoint {
  author: string;
  seq: bigint;
  digest: string;
  relay: Uint8Array;
  sig: Uint8Array;
}

export function checkpointPayload(author: string, seq: bigint, digest: string): Uint8Array {
  return taggedBytes('CKP1', lenPrefixed(utf8(author)), u64be(seq), fromHex(digest));
}

export function makeCheckpoint(relayKp: KeyPair, author: string, seq: bigint, digest: string): Checkpoint {
  return {
    author,
    seq,
    digest,
    relay: relayKp.publicDer,
    sig: signBytes(relayKp.privateDer, checkpointPayload(author, seq, digest)),
  };
}

export function verifyCheckpoint(cp: Checkpoint, trustedRelayKeys: Uint8Array[]): boolean {
  const payload = checkpointPayload(cp.author, cp.seq, cp.digest);
  return trustedRelayKeys.some((k) => keyIdOf(k) === keyIdOf(cp.relay) && verifyBytes(k, payload, cp.sig));
}

export interface MediationCert {
  author: string;
  chooses: string;
  namesHeads: string[];
  observedUpToSeq: bigint;
  mediator: Uint8Array;
  sig: Uint8Array;
}

export function mediationPayload(c: MediationCert): Uint8Array {
  const heads = [...c.namesHeads].sort();
  return taggedBytes(
    'MED1',
    lenPrefixed(utf8(c.author)),
    u64be(c.observedUpToSeq),
    u32be(heads.length),
    ...heads.map((h) => fromHex(h)),
    fromHex(c.chooses),
  );
}

export function makeMediation(
  mediatorKp: KeyPair,
  author: string,
  chooses: string,
  namesHeads: string[],
  observedUpToSeq: bigint,
): MediationCert {
  const cert: MediationCert = {
    author,
    chooses,
    namesHeads: [...namesHeads].sort(),
    observedUpToSeq,
    mediator: mediatorKp.publicDer,
    sig: new Uint8Array(),
  };
  cert.sig = signBytes(mediatorKp.privateDer, mediationPayload(cert));
  return cert;
}

export function verifyMediation(cert: MediationCert, trustedMediatorKeys: Uint8Array[]): boolean {
  const payload = mediationPayload(cert);
  return trustedMediatorKeys.some(
    (k) => keyIdOf(k) === keyIdOf(cert.mediator) && verifyBytes(k, payload, cert.sig),
  );
}
