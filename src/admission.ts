import { createHash } from 'node:crypto';
import { BootstrapRegistry } from './bootstrap.ts';
import { canonicalName, namesEqual } from './punyname.ts';
import { isPrivateAddress } from './ipaddr.ts';
import { NegativeCache, TtlCache } from './cache.ts';

export interface FetchResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
}

export type FetchLike = (url: string) => Promise<FetchResponse>;
export type ResolverLike = (host: string) => Promise<string[]>;

export interface AdmissionOptions {
  bootstrap: BootstrapRegistry;
  fetch: FetchLike;
  whitelist?: string[];
  resolver?: ResolverLike;
  maxRedirects?: number;
  expectedObjectClass?: string;
  requiredConformance?: string[];
  allowedLinkRels?: string[];
  positiveTtlMs?: number;
  negativeTtlMs?: number;
  now?: () => number;
}

export type AdmissionResult =
  | {
      ok: true;
      name: string;
      response: Record<string, unknown>;
      chain: string[];
      fromCache: boolean;
    }
  | {
      ok: false;
      name: string;
      reason: string;
      chain: string[];
      fromCache: boolean;
    };

interface ChainSuccess {
  ok: true;
  response: FetchResponse;
  chain: string[];
}

interface ChainFailure {
  ok: false;
  reason: string;
  chain: string[];
}

const REDIRECT_STATUSES = new Set([301, 302, 303, 307, 308]);

const DEFAULT_LINK_RELS = [
  'self',
  'related',
  'alternate',
  'canonical',
  'up',
  'describedby',
  'about',
  'help',
  'terms-of-service',
  'copyright',
];

function sha256Hex(body: string): string {
  return createHash('sha256').update(body).digest('hex');
}

function normalizeHost(host: string): string {
  let h = host.toLowerCase();
  if (h.endsWith('.')) h = h.slice(0, -1);
  if (h.startsWith('[')) return h;
  return canonicalName(h) ?? h;
}

export class RdapAdmission {
  #bootstrap: BootstrapRegistry;
  #fetch: FetchLike;
  #resolver: ResolverLike | null;
  #allowedHosts: Set<string>;
  #maxRedirects: number;
  #expectedObjectClass: string;
  #requiredConformance: string[];
  #allowedLinkRels: Set<string>;
  #positiveTtlMs: number;
  #negativeTtlMs: number;
  #positive: TtlCache<Record<string, unknown>>;
  #negative: NegativeCache;

  constructor(options: AdmissionOptions) {
    this.#bootstrap = options.bootstrap;
    this.#fetch = options.fetch;
    this.#resolver = options.resolver ?? null;
    this.#allowedHosts = new Set(this.#bootstrap.hosts());
    for (const host of options.whitelist ?? []) {
      this.#allowedHosts.add(normalizeHost(host));
    }
    this.#maxRedirects = options.maxRedirects ?? 3;
    this.#expectedObjectClass = options.expectedObjectClass ?? 'domain';
    this.#requiredConformance = options.requiredConformance ?? ['rdap_level_0'];
    this.#allowedLinkRels = new Set(options.allowedLinkRels ?? DEFAULT_LINK_RELS);
    this.#positiveTtlMs = options.positiveTtlMs ?? 5 * 60 * 1000;
    this.#negativeTtlMs = options.negativeTtlMs ?? 60 * 1000;
    const now = options.now ?? (() => Date.now());
    this.#positive = new TtlCache(now);
    this.#negative = new NegativeCache(now);
  }

  get positiveCache(): TtlCache<Record<string, unknown>> {
    return this.#positive;
  }

  get negativeCache(): NegativeCache {
    return this.#negative;
  }

  async query(name: string): Promise<AdmissionResult> {
    const canon = canonicalName(name);
    if (!canon) {
      return { ok: false, name: '', reason: 'invalid-query-name', chain: [], fromCache: false };
    }
    const cached = this.#positive.get(canon);
    if (cached) {
      return { ok: true, name: canon, response: cached, chain: [], fromCache: true };
    }
    const baseUrls = this.#bootstrap.baseUrlsForName(canon);
    if (baseUrls.length === 0) {
      return { ok: false, name: canon, reason: 'no-bootstrap-server', chain: [], fromCache: false };
    }
    let lastFailure: AdmissionResult | null = null;
    for (const base of baseUrls) {
      const root = base.endsWith('/') ? base : `${base}/`;
      const url = new URL(`domain/${encodeURIComponent(canon)}`, root).href;
      const result = await this.#fetchAndValidate(canon, url);
      if (result.ok) return result;
      lastFailure = result;
    }
    return lastFailure as AdmissionResult;
  }

  async follow(url: string): Promise<ChainSuccess | ChainFailure> {
    return this.#followChain(url);
  }

  async #fetchAndValidate(canon: string, url: string): Promise<AdmissionResult> {
    const chainResult = await this.#followChain(url);
    if (!chainResult.ok) {
      return { ok: false, name: canon, reason: chainResult.reason, chain: chainResult.chain, fromCache: false };
    }
    const { response, chain } = chainResult;
    const bodyHash = sha256Hex(response.body);
    const negativeHit = this.#negative.get(canon, bodyHash);
    if (negativeHit) {
      return { ok: false, name: canon, reason: negativeHit.reason, chain, fromCache: true };
    }
    const fail = (reason: string): AdmissionResult => {
      this.#negative.set(canon, bodyHash, reason, this.#negativeTtlMs);
      return { ok: false, name: canon, reason, chain, fromCache: false };
    };
    if (response.status < 200 || response.status >= 300) {
      return fail('http-error');
    }
    let doc: unknown;
    try {
      doc = JSON.parse(response.body);
    } catch {
      return fail('invalid-json');
    }
    if (typeof doc !== 'object' || doc === null || Array.isArray(doc)) {
      return fail('invalid-json');
    }
    const obj = doc as Record<string, unknown>;
    if (obj.objectClassName !== this.#expectedObjectClass) {
      return fail('unexpected-object-class');
    }
    const conformance = obj.rdapConformance;
    if (
      !Array.isArray(conformance) ||
      !this.#requiredConformance.every((r) => conformance.includes(r))
    ) {
      return fail('missing-conformance');
    }
    if (typeof obj.ldhName !== 'string' || !namesEqual(obj.ldhName, canon)) {
      return fail('name-mismatch');
    }
    const links = Array.isArray(obj.links) ? obj.links : [];
    const cleanLinks: unknown[] = [];
    for (const link of links) {
      const clean = await this.#sanitizeLink(link);
      if (clean) cleanLinks.push(clean);
    }
    const sanitized = { ...obj, links: cleanLinks };
    this.#positive.set(canon, sanitized, this.#positiveTtlMs);
    return { ok: true, name: canon, response: sanitized, chain, fromCache: false };
  }

  async #followChain(startUrl: string): Promise<ChainSuccess | ChainFailure> {
    const visited = new Set<string>();
    const chain: string[] = [];
    let url = startUrl;
    for (let redirects = 0; ; redirects++) {
      const hopError = await this.#checkHop(url);
      if (hopError) return { ok: false, reason: hopError, chain };
      let normalized: string;
      try {
        normalized = new URL(url).href;
      } catch {
        return { ok: false, reason: 'invalid-url', chain };
      }
      if (visited.has(normalized)) {
        return { ok: false, reason: 'redirect-loop', chain };
      }
      visited.add(normalized);
      chain.push(url);
      let response: FetchResponse;
      try {
        response = await this.#fetch(url);
      } catch {
        return { ok: false, reason: 'fetch-failed', chain };
      }
      if (REDIRECT_STATUSES.has(response.status)) {
        if (redirects >= this.#maxRedirects) {
          return { ok: false, reason: 'too-many-redirects', chain };
        }
        const location = response.headers['location'];
        if (!location) return { ok: false, reason: 'http-error', chain };
        try {
          url = new URL(location, url).href;
        } catch {
          return { ok: false, reason: 'invalid-url', chain };
        }
        continue;
      }
      return { ok: true, response, chain };
    }
  }

  async #checkHop(url: string): Promise<string | null> {
    let parsed: URL;
    try {
      parsed = new URL(url);
    } catch {
      return 'invalid-url';
    }
    if (parsed.protocol !== 'https:') return 'insecure-scheme';
    if (isPrivateAddress(parsed.hostname)) return 'private-address';
    const host = normalizeHost(parsed.hostname);
    if (!this.#allowedHosts.has(host)) return 'host-not-allowed';
    if (this.#resolver) {
      let addresses: string[];
      try {
        addresses = await this.#resolver(parsed.hostname);
      } catch {
        return 'dns-failed';
      }
      if (addresses.some((addr) => isPrivateAddress(addr))) {
        return 'private-address';
      }
    }
    return null;
  }

  async #sanitizeLink(link: unknown): Promise<Record<string, unknown> | null> {
    if (typeof link !== 'object' || link === null || Array.isArray(link)) return null;
    const entry = link as Record<string, unknown>;
    if (typeof entry.href !== 'string') return null;
    const rel = typeof entry.rel === 'string' ? entry.rel : '';
    if (!this.#allowedLinkRels.has(rel)) return null;
    let parsed: URL;
    try {
      parsed = new URL(entry.href);
    } catch {
      return null;
    }
    if (parsed.protocol !== 'https:') return null;
    const host = normalizeHost(parsed.hostname);
    if (!this.#allowedHosts.has(host)) return null;
    if (isPrivateAddress(parsed.hostname)) return null;
    if (this.#resolver) {
      let addresses: string[];
      try {
        addresses = await this.#resolver(parsed.hostname);
      } catch {
        return null;
      }
      if (addresses.some((addr) => isPrivateAddress(addr))) return null;
    }
    return entry;
  }
}
