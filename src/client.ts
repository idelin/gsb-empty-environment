import {
  BootstrapRegistry,
} from "./bootstrap.ts";
import {
  admitResponse,
  buildRdapUrl,
  DOMAIN_ADMISSION_POLICY,
  filterFollowableLinks,
  type AdmissionPolicy,
  type AdmissionResult,
  type RdapResponse,
} from "./admission.ts";
import { normalizeDomain } from "./domain.ts";
import { evaluateUrl } from "./netsec.ts";

/**
 * 供查询流程直接调用的准入客户端。把引导来源、links/重定向出口约束、
 * 实体一致性校验与“仅缓存已准入应答”的策略封装在一起。
 */

export const MAX_REDIRECTS = 3;

export type QueryFailure =
  | "invalid-query-name"
  | "bootstrap-missing"
  | "bootstrap-not-loaded"
  | "request-failed"
  | "redirect-limit-exceeded"
  | "redirect-not-https"
  | "redirect-host-not-allowed"
  | "redirect-private-address"
  | "bad-status"
  | "admission-rejected";

export class RdapAdmissionError extends Error {
  readonly failure: QueryFailure;
  constructor(failure: QueryFailure, message?: string) {
    super(message ?? failure);
    this.name = "RdapAdmissionError";
    this.failure = failure;
  }
}

export interface CachedEntry {
  body: RdapResponse;
  normalizedName: string;
  cachedAt: number;
}

/**
 * 极简缓存。只暴露 admit/写入路径，确保未通过准入的对象永远进不来。
 */
export class AdmissionCache {
  private readonly store = new Map<string, CachedEntry>();

  private readonly ttlMs: number;

  constructor(ttlMs: number = 15 * 60 * 1000) {
    this.ttlMs = ttlMs;
  }

  get(normalizedName: string): CachedEntry | null {
    const hit = this.store.get(normalizedName);
    if (!hit) {
      return null;
    }
    if (Date.now() - hit.cachedAt > this.ttlMs) {
      this.store.delete(normalizedName);
      return null;
    }
    return hit;
  }

  /** 仅允许缓存已通过 admitResponse 的对象。 */
  put(normalizedName: string, body: RdapResponse, result: AdmissionResult): void {
    if (!result.admitted) {
      return; // 不一致整条丢弃且不入缓存。
    }
    this.store.set(normalizedName, {
      body,
      normalizedName,
      cachedAt: Date.now(),
    });
  }

  delete(normalizedName: string): void {
    this.store.delete(normalizedName);
  }

  clear(): void {
    this.store.clear();
  }

  get size(): number {
    return this.store.size;
  }
}

export interface RdapClientOptions {
  registry: BootstrapRegistry;
  /** 调用方额外信任的主机（小写、无端口）。仍强制 https、仍禁私有地址。 */
  hostWhitelist?: Iterable<string>;
  cache?: AdmissionCache;
  fetchImpl?: typeof fetch;
  policy?: AdmissionPolicy;
  maxRedirects?: number;
}

export interface QueryOutcome {
  body: RdapResponse;
  fromCache: boolean;
  /** 本次最终命中的主机（便于审计）。 */
  resolvedHost: string;
  /** 跟随 3xx 的实际次数。 */
  redirects: number;
}

export class RdapClient {
  private readonly registry: BootstrapRegistry;
  private readonly whitelist: Set<string>;
  private readonly cache: AdmissionCache;
  private readonly fetchImpl: typeof fetch;
  private readonly policy: AdmissionPolicy;
  private readonly maxRedirects: number;

  constructor(options: RdapClientOptions) {
    this.registry = options.registry;
    this.whitelist = new Set(
      (options.hostWhitelist ?? []).map((h) => h.toLowerCase().replace(/\.$/, "")),
    );
    this.cache = options.cache ?? new AdmissionCache();
    this.fetchImpl = options.fetchImpl ?? fetch;
    this.policy = options.policy ?? DOMAIN_ADMISSION_POLICY;
    this.maxRedirects = options.maxRedirects ?? MAX_REDIRECTS;
  }

  /** 引导主机 ∪ 调用方白名单。 */
  private allowedHosts(): Set<string> {
    const allowed = this.registry.allBootstrapHosts();
    for (const host of this.whitelist) {
      allowed.add(host);
    }
    return allowed;
  }

  /** 把应答里的 links 过滤成可安全跟随集合，供查询流程按需取用。 */
  safeLinks(body: RdapResponse, base?: string | URL) {
    return filterFollowableLinks(body.links, this.allowedHosts(), base);
  }

  async query(queryName: string): Promise<QueryOutcome> {
    const normalized = normalizeDomain(queryName);
    if (!normalized) {
      throw new RdapAdmissionError("invalid-query-name", `invalid domain: ${queryName}`);
    }

    const cached = this.cache.get(normalized);
    if (cached) {
      return {
        body: cached.body,
        fromCache: true,
        resolvedHost: "",
        redirects: 0,
      };
    }

    if (!this.registry.isLoaded) {
      throw new RdapAdmissionError("bootstrap-not-loaded");
    }
    const match = this.registry.match(normalized);
    if (!match) {
      // 引导缺项：绝不退化为默认/任意服务器。
      throw new RdapAdmissionError(
        "bootstrap-missing",
        `no RDAP bootstrap entry for ${normalized}`,
      );
    }

    const initial = buildRdapUrl(match.baseUrl, normalized);
    if (!initial) {
      throw new RdapAdmissionError("invalid-query-name");
    }

    const allowed = this.allowedHosts();
    let current = initial;
    let redirects = 0;
    let lastHost = current.hostname;

    // 手动处理重定向，确保每一跳都重新过 https/主机/私有地址准入，
    // 而不是把策略交给底层 fetch 的自动跳转。
    for (;;) {
      const response = await this.fetchImpl(current.toString(), {
        redirect: "manual",
      }).catch((cause: unknown) => {
        throw new RdapAdmissionError(
          "request-failed",
          cause instanceof Error ? cause.message : "network error",
        );
      });

      if (response.status >= 300 && response.status < 400 && response.headers.has("location")) {
        if (redirects >= this.maxRedirects) {
          throw new RdapAdmissionError("redirect-limit-exceeded");
        }
        const location = response.headers.get("location") ?? "";
        const verdict = evaluateUrl(location, allowed, current);
        if (!verdict.ok) {
          const failure = verdict.reason === "not-https"
            ? "redirect-not-https"
            : verdict.reason === "private-address"
              ? "redirect-private-address"
              : "redirect-host-not-allowed";
          throw new RdapAdmissionError(failure, `blocked redirect -> ${location}`);
        }
        redirects += 1;
        current = verdict.parsed!.url;
        lastHost = verdict.parsed!.host;
        continue;
      }

      if (!response.ok) {
        throw new RdapAdmissionError(
          "bad-status",
          `RDAP responded ${response.status}`,
        );
      }

      const body = (await response.json().catch(() => null)) as RdapResponse | null;
      const result = admitResponse(body, normalized, this.policy);
      if (!result.admitted) {
        // 整条丢弃；AdmissionCache.put 对未准入对象是 no-op，双保险不缓存。
        throw new RdapAdmissionError(
          "admission-rejected",
          `response rejected: ${result.violation}`,
        );
      }
      this.cache.put(normalized, body as RdapResponse, result);
      return {
        body: body as RdapResponse,
        fromCache: false,
        resolvedHost: lastHost,
        redirects,
      };
    }
  }
}
