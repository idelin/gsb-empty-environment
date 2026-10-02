import {
  createHash,
  createPublicKey,
  generateKeyPairSync,
  sign,
  verify,
  type KeyObject,
} from 'node:crypto';

export interface Identity {
  publicKey: KeyObject;
  privateKey: KeyObject;
  publicKeyPem: string;
}

export function generateIdentity(): Identity {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const publicKeyPem = publicKey.export({ type: 'spki', format: 'pem' }).toString();
  return { publicKey, privateKey, publicKeyPem };
}

export function signPayload(identity: Identity, payload: string): string {
  return sign(null, Buffer.from(payload, 'utf8'), identity.privateKey).toString('hex');
}

export function verifyPayload(
  publicKeyPem: string,
  payload: string,
  signature: string,
): boolean {
  try {
    const key = createPublicKey(publicKeyPem);
    return verify(null, Buffer.from(payload, 'utf8'), key, Buffer.from(signature, 'hex'));
  } catch {
    return false;
  }
}

export function sha256(payload: string): string {
  return createHash('sha256').update(payload, 'utf8').digest('hex');
}

export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalize(item)).join(',')}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const entries = keys.map(
    (key) => `${JSON.stringify(key)}:${canonicalize(record[key])}`,
  );
  return `{${entries.join(',')}}`;
}
