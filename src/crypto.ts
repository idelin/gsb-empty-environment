import { createHash, generateKeyPairSync, sign, verify } from 'node:crypto';

export function sha256Hex(data: string | Buffer): string {
  return createHash('sha256').update(data).digest('hex');
}

export function canonicalJson(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return '[' + value.map(canonicalJson).join(',') + ']';
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  return (
    '{' +
    keys.map((k) => JSON.stringify(k) + ':' + canonicalJson(record[k])).join(',') +
    '}'
  );
}

export interface KeyPair {
  publicKey: string;
  privateKey: string;
}

export function generateLedgerKeyPair(): KeyPair {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519', {
    publicKeyEncoding: { type: 'spki', format: 'pem' },
    privateKeyEncoding: { type: 'pkcs8', format: 'pem' },
  });
  return { publicKey, privateKey };
}

export function signHex(privateKeyPem: string, message: string): string {
  return sign(null, Buffer.from(message, 'utf8'), privateKeyPem).toString('hex');
}

export function verifySignature(
  publicKeyPem: string,
  message: string,
  signatureHex: string,
): boolean {
  try {
    return verify(
      null,
      Buffer.from(message, 'utf8'),
      publicKeyPem,
      Buffer.from(signatureHex, 'hex'),
    );
  } catch {
    return false;
  }
}
