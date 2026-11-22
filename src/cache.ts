import { createHmac, timingSafeEqual } from 'node:crypto';
import { type BDDNode, FixedOrderBDD } from './bdd.ts';
import type { SourceExpr } from './model.ts';

/**
 * One acceptable rendering configuration for an occurrence of a cached
 * fragment. It pins response id, tree position, content commitment, nonce
 * binding and inheritable sources, plus the symbolic switch condition under
 * which every composed policy accepts it.
 */
export interface SealedRenderConfig {
  responseId: string;
  positionKey: string;
  scriptId: string;
  commitment: string;
  nonce: string | undefined;
  inheritableSources: SourceExpr[];
  acceptableWhen: BDDNode;
}

export interface CacheCertificate {
  cacheId: string;
  fragmentId: string;
  issuedAt: string;
  configs: Array<Omit<SealedRenderConfig, 'acceptableWhen'> & { acceptableWhenNode: number }>;
  signature: string;
}

export type CertificateFailure =
  | 'bad-signature'
  | 'commitment-changed'
  | 'position-mismatch'
  | 'response-mismatch'
  | 'nonce-mismatch'
  | 'no-acceptable-config'
  | 'unknown-fragment';

export interface PresentedFragment {
  fragmentId: string;
  responseId: string;
  positionKey: string;
  scriptId: string;
  commitment: string;
  nonce?: string;
  /** Current symbolic condition supplied by the rendering host. */
  activeCondition: BDDNode;
  /** Shared BDD so the certified node condition can be tested symbolically. */
  bdd: FixedOrderBDD;
}

/**
 * Signs shared fragment caches. A certificate covers every acceptable
 * rendering configuration for the fragment; verification fails when the
 * presented bytes change any pinned commitment (the authorization is then
 * void) or when the host's active condition lies outside the config condition.
 */
export class CachePublisher {
  private readonly signingKey: Uint8Array;

  constructor(signingKey: Uint8Array) {
    this.signingKey = signingKey;
  }

  publish(cacheId: string, fragmentId: string, configs: SealedRenderConfig[]): CacheCertificate {
    const certificate: CacheCertificate = {
      cacheId,
      fragmentId,
      issuedAt: new Date(0).toISOString(),
      configs: configs.map((config) => ({
        responseId: config.responseId,
        positionKey: config.positionKey,
        scriptId: config.scriptId,
        commitment: config.commitment,
        nonce: config.nonce,
        inheritableSources: config.inheritableSources.map((source) => ({ ...source })),
        acceptableWhenNode: config.acceptableWhen,
      })),
      signature: '',
    };
    certificate.signature = this.sign(certificate);
    return certificate;
  }

  verify(certificate: CacheCertificate, presented: PresentedFragment): { ok: true } | { ok: false; reason: CertificateFailure; detail: string } {
    const expectedSignature = this.sign(certificate);
    if (!safeEqual(certificate.signature, expectedSignature)) {
      return { ok: false, reason: 'bad-signature', detail: 'certificate signature does not match' };
    }
    if (certificate.fragmentId !== presented.fragmentId) {
      return { ok: false, reason: 'unknown-fragment', detail: presented.fragmentId };
    }
    const matching = certificate.configs.filter(
      (config) =>
        config.positionKey === presented.positionKey &&
        config.scriptId === presented.scriptId,
    );
    if (matching.length === 0) {
      return { ok: false, reason: 'position-mismatch', detail: presented.positionKey };
    }
    for (const config of matching) {
      if (config.responseId !== presented.responseId) {
        return { ok: false, reason: 'response-mismatch', detail: config.responseId };
      }
      if (config.commitment !== presented.commitment) {
        return {
          ok: false,
          reason: 'commitment-changed',
          detail: `pinned ${config.commitment} vs presented ${presented.commitment}`,
        };
      }
      if ((config.nonce ?? '') !== (presented.nonce ?? '')) {
        return { ok: false, reason: 'nonce-mismatch', detail: 'nonce binding changed' };
      }
      const allowed = config.acceptableWhenNode;
      if (!presented.bdd.entails(presented.activeCondition, allowed)) {
        return { ok: false, reason: 'no-acceptable-config', detail: 'active switches outside certified condition' };
      }
      return { ok: true };
    }
    return { ok: false, reason: 'no-acceptable-config', detail: 'no config matched the presented occurrence' };
  }

  private sign(certificate: CacheCertificate): string {
    const payload = canonicalJson({
      cacheId: certificate.cacheId,
      fragmentId: certificate.fragmentId,
      issuedAt: certificate.issuedAt,
      configs: certificate.configs,
    });
    return createHmac('sha256', this.signingKey).update(payload).digest('base64url');
  }
}

function canonicalJson(value: unknown): string {
  return JSON.stringify(sortDeep(value));
}

function sortDeep(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(sortDeep);
  }
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([key, nested]) => [key, sortDeep(nested)]),
    );
  }
  return value;
}

function safeEqual(a: string, b: string): boolean {
  const bufA = Buffer.from(a);
  const bufB = Buffer.from(b);
  return bufA.length === bufB.length && timingSafeEqual(bufA, bufB);
}
