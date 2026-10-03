import crypto from 'node:crypto';
import {
  eciesDecrypt,
  generateEcKeyPair,
  signPayload,
  verifySignature,
  type EciesBox,
} from './crypto.ts';

export interface CredentialClaims {
  credentialId: string;
  issuer: string;
  targetRealId: string;
  from: number;
  to: number;
  purpose: string;
  expiresAt: number;
  nonce: string;
}

export interface AuditCredential extends CredentialClaims {
  signature: string;
}

export function canonicalClaims(claims: CredentialClaims): string {
  return JSON.stringify({
    credentialId: claims.credentialId,
    issuer: claims.issuer,
    targetRealId: claims.targetRealId,
    from: claims.from,
    to: claims.to,
    purpose: claims.purpose,
    expiresAt: claims.expiresAt,
    nonce: claims.nonce,
  });
}

export class AuditAuthority {
  readonly publicKeyPem: string;
  private readonly privateKeyPem: string;

  private constructor(publicKeyPem: string, privateKeyPem: string) {
    this.publicKeyPem = publicKeyPem;
    this.privateKeyPem = privateKeyPem;
  }

  static generate(): AuditAuthority {
    const { publicKeyPem, privateKeyPem } = generateEcKeyPair();
    return new AuditAuthority(publicKeyPem, privateKeyPem);
  }

  issueCredential(params: {
    issuer: string;
    targetRealId: string;
    from: number;
    to: number;
    purpose: string;
    ttlMs: number;
    now?: number;
  }): AuditCredential {
    const now = params.now ?? Date.now();
    if (params.to < params.from) {
      throw new Error('credential time range is inverted');
    }
    const claims: CredentialClaims = {
      credentialId: crypto.randomUUID(),
      issuer: params.issuer,
      targetRealId: params.targetRealId,
      from: params.from,
      to: params.to,
      purpose: params.purpose,
      expiresAt: now + params.ttlMs,
      nonce: crypto.randomBytes(16).toString('hex'),
    };
    return { ...claims, signature: signPayload(this.privateKeyPem, canonicalClaims(claims)) };
  }

  get revealKeyPem(): string {
    return this.privateKeyPem;
  }

  decryptPayload(box: EciesBox): Buffer {
    return eciesDecrypt(this.privateKeyPem, box);
  }
}

export function verifyCredential(
  credential: AuditCredential,
  authorityPublicKeyPem: string,
  now = Date.now(),
): void {
  const { signature, ...claims } = credential;
  if (!verifySignature(authorityPublicKeyPem, canonicalClaims(claims), signature)) {
    throw new Error('credential signature invalid');
  }
  if (now > credential.expiresAt) {
    throw new Error('credential expired');
  }
  if (credential.to < credential.from) {
    throw new Error('credential time range is inverted');
  }
}
