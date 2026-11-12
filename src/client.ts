/**
 * MTA-STS delivery decision engine (RFC 8461 §7).
 *
 * Policy updates only happen over HTTPS and only when the policy id changes.
 * In `enforce` mode an MX host must match a policy `mx` entry and its
 * certificate identity must match that MX host; in `testing` mode failures
 * are recorded but delivery is not blocked.
 */

import { certificateMatchesMx, mxMatchesPolicyEntry, normalizeHostname } from "./hostname.ts";
import { parsePolicy, parseStsTxtRecords } from "./policy.ts";
import type { MtaStsPolicy } from "./policy.ts";
import type { CachedPolicy, PolicyCache } from "./cache.ts";
import { InMemoryPolicyCache } from "./cache.ts";

export interface DnsResolver {
  /** Raw TXT records at `_mta-sts.<domain>`; an empty array means absent. */
  resolveStsTxt(domain: string): Promise<string[]>;
}

export interface HttpsResponse {
  url: string;
  status: number;
  body: string;
  /**
   * Redirect chain actually followed by the fetcher. Every hop must remain
   * on https; the default fetcher never follows http(s) downgrade redirects.
   */
  redirectUrls?: string[];
}

export interface HttpsFetcher {
  fetch(url: string): Promise<HttpsResponse>;
}

export interface Logger {
  warn(message: string, details?: Record<string, unknown>): void;
}

export const consoleLogger: Logger = {
  warn(message, details) {
    if (details === undefined) {
      console.warn(`[mta-sts] ${message}`);
    } else {
      console.warn(`[mta-sts] ${message}`, details);
    }
  },
};

/**
 * Default fetcher backed by global fetch. Refuses non-HTTPS URLs and any
 * redirect (even cross-host on https), matching the strict guidance of
 * RFC 8461 §3.3.
 */
export class DefaultHttpsFetcher implements HttpsFetcher {
  async fetch(url: string): Promise<HttpsResponse> {
    if (!url.startsWith("https://")) {
      throw new Error(`refusing to fetch MTA-STS policy over non-HTTPS URL: ${url}`);
    }
    const response = await fetch(url, {
      method: "GET",
      redirect: "error",
      cache: "no-store",
    });
    if (!response.ok) {
      throw new Error(`MTA-STS policy fetch failed with status ${response.status}`);
    }
    return {
      url: response.url || url,
      status: response.status,
      body: await response.text(),
    };
  }
}

export type DeliveryAction = "deliver" | "block";

export type MtaStsReason =
  | "no-policy"
  | "policy-mode-none"
  | "mx-match"
  | "mx-mismatch"
  | "certificate-mismatch"
  | "policy-fetch-failure"
  | "dns-error";

export interface EvaluationInput {
  domain: string;
  /** Candidate MX host selected by normal MX resolution. */
  mxHost: string;
  /** DNS names (SANs, then CN fallback) presented by the MX certificate. */
  certificateNames: readonly string[];
}

export interface EvaluationResult {
  action: DeliveryAction;
  reason: MtaStsReason;
  mode: MtaStsPolicy["mode"] | null;
  /** Policy that drove the decision, when one was available. */
  policy: MtaStsPolicy | null;
  /** True when delivery is allowed only because the policy is in testing mode. */
  testingOverride: boolean;
  detail?: string;
}

export interface MtaStsClientOptions {
  resolver: DnsResolver;
  fetcher?: HttpsFetcher;
  cache?: PolicyCache;
  logger?: Logger;
  /** Injected clock, mainly for tests. */
  now?: () => number;
}

export class MtaStsClient {
  private readonly resolver: DnsResolver;
  private readonly fetcher: HttpsFetcher;
  private readonly cache: PolicyCache;
  private readonly logger: Logger;
  private readonly now: () => number;

  constructor(options: MtaStsClientOptions) {
    this.resolver = options.resolver;
    this.fetcher = options.fetcher ?? new DefaultHttpsFetcher();
    this.cache = options.cache ?? new InMemoryPolicyCache();
    this.logger = options.logger ?? consoleLogger;
    this.now = options.now ?? (() => Date.now());
  }

  getCache(): PolicyCache {
    return this.cache;
  }

  /**
   * Evaluate whether delivery to `mxHost` is permitted under the domain's
   * current MTA-STS policy.
   */
  async evaluate(input: EvaluationInput): Promise<EvaluationResult> {
    const domain = normalizeHostname(input.domain);
    const mxHost = normalizeHostname(input.mxHost);
    const now = this.now();

    let txtRecords: string[];
    try {
      txtRecords = await this.resolver.resolveStsTxt(domain);
    } catch (error) {
      // Failing closed: a DNS lookup error must not be treated as "no policy".
      const cached = this.cache.get(domain, now);
      return this.decideWithCache(
        input,
        domain,
        mxHost,
        cached,
        "dns-error",
        error instanceof Error ? error.message : String(error),
      );
    }

    const txt = parseStsTxtRecords(txtRecords);
    const cached = this.cache.get(domain, now);

    if (txt === null) {
      // Record removed or stripped: keep using the cached policy until max_age.
      if (cached !== null) {
        return this.applyPolicy(input, mxHost, cached);
      }
      return {
        action: "deliver",
        reason: "no-policy",
        mode: null,
        policy: null,
        testingOverride: false,
      };
    }

    // RFC 8461 §7.2: fetch only when there is no fresh cache or the id changed.
    if (cached === null || cached.id !== txt.id) {
      try {
        const policy = await this.fetchPolicy(domain);
        this.cache.set(domain, txt.id, policy, now);
        return this.applyPolicy(input, mxHost, this.cache.get(domain, now)!);
      } catch (error) {
        // A bad/failed fetch never removes or replaces the cached policy.
        if (cached !== null) {
          return this.applyPolicy(input, mxHost, cached);
        }
        return {
          action: "block",
          reason: "policy-fetch-failure",
          mode: null,
          policy: null,
          testingOverride: false,
          detail: error instanceof Error ? error.message : String(error),
        };
      }
    }

    return this.applyPolicy(input, mxHost, cached);
  }

  private decideWithCache(
    input: EvaluationInput,
    domain: string,
    mxHost: string,
    cached: CachedPolicy | null,
    reason: MtaStsReason,
    detail: string,
  ): EvaluationResult {
    if (cached !== null) {
      return this.applyPolicy(input, mxHost, cached);
    }
    this.logger.warn(`MTA-STS lookup failed for ${domain}`, { reason, detail });
    return {
      action: "block",
      reason,
      mode: null,
      policy: null,
      testingOverride: false,
      detail,
    };
  }

  /** Enforce/testing/none decision against a concrete cached policy. */
  private applyPolicy(
    input: EvaluationInput,
    mxHost: string,
    cached: CachedPolicy,
  ): EvaluationResult {
    const { policy } = cached;

    if (policy.mode === "none") {
      return {
        action: "deliver",
        reason: "policy-mode-none",
        mode: policy.mode,
        policy,
        testingOverride: false,
      };
    }

    const mxAllowed = policy.mx.some((entry) => mxMatchesPolicyEntry(mxHost, entry));
    if (!mxAllowed) {
      return this.policyFailure(cached, "mx-mismatch", `MX ${mxHost} is not listed by policy`);
    }

    const certAllowed = certificateMatchesMx(mxHost, input.certificateNames);
    if (!certAllowed) {
      return this.policyFailure(
        cached,
        "certificate-mismatch",
        `certificate for ${mxHost} does not present a matching identity`,
      );
    }

    return {
      action: "deliver",
      reason: "mx-match",
      mode: policy.mode,
      policy,
      testingOverride: false,
    };
  }

  private policyFailure(
    cached: CachedPolicy,
    reason: MtaStsReason,
    detail: string,
  ): EvaluationResult {
    const { policy } = cached;
    if (policy.mode === "testing") {
      // Testing mode: record only, never block.
      this.logger.warn("MTA-STS testing-mode failure", {
        reason,
        detail,
        mode: policy.mode,
      });
      return {
        action: "deliver",
        reason,
        mode: policy.mode,
        policy,
        testingOverride: true,
        detail,
      };
    }
    return {
      action: "block",
      reason,
      mode: policy.mode,
      policy,
      testingOverride: false,
      detail,
    };
  }

  private async fetchPolicy(domain: string): Promise<MtaStsPolicy> {
    const url = `https://mta-sts.${domain}/.well-known/mta-sts.txt`;
    const response = await this.fetcher.fetch(url);
    if (!response.url.startsWith("https://")) {
      throw new Error(`policy was not served over HTTPS: ${response.url}`);
    }
    if (response.status < 200 || response.status >= 300) {
      throw new Error(`policy fetch returned status ${response.status}`);
    }
    return parsePolicy(response.body);
  }
}
