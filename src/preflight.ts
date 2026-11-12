import {
  RebindingDetectedError,
  PrivateNetworkAccessDeniedError,
} from './errors.ts';
import { classifyIp, isMorePublic, normalizeIp, type IpSpace } from './ip.ts';

export interface GuardRequest {
  /** Full target URL of the request that needs protection. */
  readonly url: string;
  /** HTTP method of the actual request. */
  readonly method: string;
  /**
   * Actual remote peer IP observed for this connection attempt. Never pass a
   * host name here: DNS rebinding swaps host names freely, but the peer IP is
   * what the socket actually connected to.
   */
  readonly targetIp: string;
  /**
   * Address space of the initiating context (the page/fetch client). May be
   * passed pre-classified or as an initiator peer IP literal.
   */
  readonly initiator: IpSpace | string;
}

export interface PreflightResponse {
  readonly status: number;
  readonly headers: Readonly<Record<string, string>>;
}

/** Pluggable transport: the browser network stack supplies its own sender. */
export interface PreflightTransport {
  sendPreflight(request: GuardRequest, headers: Record<string, string>): Promise<PreflightResponse>;
}

export interface GuardOptions {
  readonly transport: PreflightTransport;
  /** Default cache lifetime in milliseconds when Max-Age is absent. */
  readonly defaultTtlMs?: number;
  /** Upper bound applied to Access-Control-Max-Age, in milliseconds. */
  readonly maxTtlMs?: number;
  readonly now?: () => number;
}

export type Authorization =
  | { readonly preflightRequired: false }
  | {
      readonly preflightRequired: true;
      readonly cached: boolean;
      /** Canonical target IP the preflight was authorized for. */
      readonly targetIp: string;
      readonly expiresAt: number;
    };

interface CacheEntry {
  readonly targetIp: string;
  readonly expiresAt: number;
}

export const DEFAULT_TTL_MS = 10_000;
export const MAX_TTL_MS = 7_200_000;

const ALLOW_PRIVATE_NETWORK = 'access-control-allow-private-network';
const MAX_AGE = 'access-control-max-age';

/**
 * Build the preflight cache key. The target IP is part of the key (alongside
 * origin and method), so an entry authorized for one peer IP can never be
 * reused after DNS rebinding points the same origin at another IP.
 */
export function buildPreflightCacheKey(
  targetOrigin: string,
  method: string,
  targetIp: string,
): string {
  return [targetOrigin, method.toUpperCase(), normalizeIp(targetIp)].join('\n');
}

function headerValue(headers: Readonly<Record<string, string>>, name: string): string | undefined {
  const lower = name.toLowerCase();
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === lower) {
      return value;
    }
  }
  return undefined;
}

export class PrivateNetworkAccessGuard {
  private readonly transport: PreflightTransport;
  private readonly defaultTtlMs: number;
  private readonly maxTtlMs: number;
  private readonly now: () => number;
  private readonly cache = new Map<string, CacheEntry>();

  constructor(options: GuardOptions) {
    this.transport = options.transport;
    this.defaultTtlMs = options.defaultTtlMs ?? DEFAULT_TTL_MS;
    this.maxTtlMs = options.maxTtlMs ?? MAX_TTL_MS;
    this.now = options.now ?? (() => Date.now());
  }

  /**
   * Authorize a request. Resolves without a preflight for same-space or
   * less-public initiators; otherwise performs (or reuses a cached) PNA
   * preflight and rejects when the target does not opt in.
   */
  async authorize(request: GuardRequest): Promise<Authorization> {
    const targetSpace = classifyIp(request.targetIp);
    const initiatorSpace = resolveInitiatorSpace(request.initiator);

    if (!isMorePublic(initiatorSpace, targetSpace)) {
      return { preflightRequired: false };
    }

    const targetOrigin = new URL(request.url).origin;
    const canonicalIp = normalizeIp(request.targetIp);
    const key = buildPreflightCacheKey(targetOrigin, request.method, canonicalIp);

    const cached = this.cache.get(key);
    if (cached !== undefined && cached.expiresAt > this.now()) {
      return {
        preflightRequired: true,
        cached: true,
        targetIp: cached.targetIp,
        expiresAt: cached.expiresAt,
      };
    }
    this.cache.delete(key);

    const preflightHeaders: Record<string, string> = {
      'Access-Control-Request-Method': request.method.toUpperCase(),
      'Access-Control-Request-Private-Network': 'true',
    };
    const response = await this.transport.sendPreflight(request, preflightHeaders);

    if (response.status < 200 || response.status >= 300) {
      throw new PrivateNetworkAccessDeniedError('bad-status', response.status);
    }
    const allowValue = headerValue(response.headers, ALLOW_PRIVATE_NETWORK);
    if (allowValue === undefined) {
      throw new PrivateNetworkAccessDeniedError(
        'missing-allow-private-network',
        response.status,
      );
    }
    if (allowValue.trim().toLowerCase() !== 'true') {
      throw new PrivateNetworkAccessDeniedError(
        'invalid-allow-private-network',
        response.status,
      );
    }

    const ttlMs = this.resolveTtl(headerValue(response.headers, MAX_AGE));
    const expiresAt = this.now() + ttlMs;
    this.cache.set(key, { targetIp: canonicalIp, expiresAt });

    return {
      preflightRequired: true,
      cached: false,
      targetIp: canonicalIp,
      expiresAt,
    };
  }

  /**
   * Compare the IP the real connection landed on against the IP that was
   * authorized during the preflight. A mismatch means DNS rebinding (or a
   * changed route) happened between the two steps: the caller must abort the
   * connection instead of sending any data.
   */
  verifyConnection(authorization: Authorization, actualPeerIp: string): void {
    if (!authorization.preflightRequired) {
      return;
    }
    const actual = normalizeIp(actualPeerIp);
    if (actual !== authorization.targetIp) {
      throw new RebindingDetectedError(authorization.targetIp, actual);
    }
  }

  clearCache(): void {
    this.cache.clear();
  }

  private resolveTtl(maxAge: string | undefined): number {
    if (maxAge === undefined) {
      return this.defaultTtlMs;
    }
    const seconds = Number(maxAge);
    if (!Number.isFinite(seconds) || seconds <= 0) {
      return this.defaultTtlMs;
    }
    return Math.min(this.maxTtlMs, seconds * 1000);
  }
}

const SPACE_NAMES = new Set<IpSpace>(['loopback', 'private', 'public']);

function resolveInitiatorSpace(initiator: IpSpace | string): IpSpace {
  if (typeof initiator !== 'string') {
    return initiator;
  }
  if (SPACE_NAMES.has(initiator as IpSpace)) {
    return initiator as IpSpace;
  }
  return classifyIp(initiator);
}
