import { generateKeyPairSync, sign as nodeSign, verify as nodeVerify } from 'node:crypto';

export interface SignatureEnvelope {
  alg: 'ed25519';
  keyId?: string;
  publicKeyPem?: string;
  signatureBase64: string;
}

export interface DomainKeyPair {
  domain: string;
  keyId?: string;
  publicKeyPem: string;
  privateKeyPem: string;
}

export type CanonicalValue =
  | string
  | number
  | boolean
  | null
  | CanonicalValue[]
  | { [key: string]: CanonicalValue };

export function canonicalize(value: unknown): CanonicalValue {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') {
    return value as CanonicalValue;
  }
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new RangeError('cannot canonicalize non-finite number');
    return Number.isInteger(value) ? value : value;
  }
  if (Array.isArray(value)) return value.map((item) => canonicalize(item));
  if (value instanceof Uint8Array) {
    return Array.from(value).map((item) => item) as unknown as CanonicalValue;
  }
  if (typeof value === 'object') {
    const record = value as Record<string, unknown>;
    return Object.keys(record)
      .sort()
      .reduce<{ [key: string]: CanonicalValue }>((result, key) => {
        if (record[key] !== undefined) result[key] = canonicalize(record[key]);
        return result;
      }, {});
  }
  throw new TypeError(`cannot canonicalize value of type ${typeof value}`);
}

export function canonicalJSON(value: unknown): string {
  return JSON.stringify(canonicalize(value));
}

export function canonicalBytes(value: unknown): Uint8Array {
  return new TextEncoder().encode(canonicalJSON(value));
}

export function generateDomainKeyPair(domain: string, keyId?: string): DomainKeyPair {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  return {
    domain,
    keyId,
    publicKeyPem: publicKey.export({ type: 'spki', format: 'pem' }) as string,
    privateKeyPem: privateKey.export({ type: 'pkcs8', format: 'pem' }) as string,
  };
}

const nodeBuffer = (globalThis as { Buffer?: any }).Buffer;

export function signCanonical(
  payload: unknown,
  privateKeyPem: string,
  keyId?: string,
  publicKeyPem?: string,
): SignatureEnvelope {
  const signature = nodeSign(null, canonicalBytes(payload) as any, privateKeyPem as any) as {
    toString(encoding: string): string;
  };
  return {
    alg: 'ed25519',
    keyId,
    publicKeyPem,
    signatureBase64: signature.toString('base64'),
  };
}

export function verifyCanonical(
  payload: unknown,
  envelope: SignatureEnvelope,
  publicKeyPem: string,
): boolean {
  if (envelope.alg !== 'ed25519') return false;
  try {
    return nodeVerify(
      null,
      canonicalBytes(payload) as any,
      publicKeyPem as any,
      nodeBuffer.from(envelope.signatureBase64, 'base64'),
    );
  } catch {
    return false;
  }
}

export interface SignatureVerificationRequest {
  domain: string;
  payload: unknown;
  envelope: SignatureEnvelope;
}

export type SignatureVerifier = (request: SignatureVerificationRequest) => boolean;

export function ed25519PublicKeyVerifier(
  publicKeys: Readonly<Record<string, string>>,
): SignatureVerifier {
  return ({ domain, payload, envelope }) => {
    const publicKey = publicKeys[domain];
    if (!publicKey) return false;
    return verifyCanonical(payload, envelope, publicKey);
  };
}

export function base64ToBytes(base64: string): Uint8Array {
  return new Uint8Array(nodeBuffer.from(base64, 'base64'));
}

export function bytesToBase64(bytes: Uint8Array): string {
  return nodeBuffer.from(bytes).toString('base64');
}

export function timingSafeEqualBase64(left: string, right: string): boolean {
  const leftBytes = base64ToBytes(left);
  const rightBytes = base64ToBytes(right);
  if (leftBytes.length !== rightBytes.length) return false;
  let difference = 0;
  for (let index = 0; index < leftBytes.length; index += 1) {
    difference |= leftBytes[index]! ^ rightBytes[index]!;
  }
  return difference === 0;
}
