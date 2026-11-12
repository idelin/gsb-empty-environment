/**
 * W3C Network Error Logging (NEL) policy store for a browser network stack.
 *
 * Security invariants enforced here:
 * - NEL headers are only honored on HTTPS responses (cleartext injection is dropped).
 * - Responses for IP-literal hosts never set policies.
 * - `report_to` must reference a group declared in the SAME response's Report-To header.
 * - `success_fraction` / `failure_fraction` outside [0, 1] invalidate the whole policy.
 * - `max_age: 0` removes the policy for that origin.
 * - Policies are keyed by exact origin; a subdomain's policy never affects its parent.
 * - When a policy with `include_subdomains` matches a subdomain request, the report
 *   `url` is reduced to the request's origin (per this task's rule).
 * - Reports are only delivered to the endpoints of the Report-To group that the
 *   policy itself referenced.
 */

export interface ReportToEndpoint {
  url: string;
}

export interface ReportToGroup {
  name: string;
  maxAge: number;
  endpoints: ReportToEndpoint[];
}

export interface NelPolicy {
  /** Origin (scheme://host[:port]) that set the policy. */
  origin: string;
  reportToGroup: ReportToGroup;
  includeSubdomains: boolean;
  successFraction: number;
  failureFraction: number;
  /** Epoch milliseconds at which the policy expires. */
  expiresAt: number;
}

export interface NelReport {
  type: "network-error";
  url: string;
  referrer: string;
  samplingFraction: number;
  serverIp: string;
  protocol: string;
  method: string;
  statusCode: number;
  elapsedTime: number;
  phase: "ok" | "error";
  errorType: string | null;
  /** Endpoints this report may be delivered to (from the policy's own group). */
  deliveryEndpoints: string[];
}

export interface NelRequestInfo {
  url: string;
  method?: string;
  statusCode?: number;
  elapsedTime?: number;
  serverIp?: string;
  protocol?: string;
  referrer?: string;
  errorType?: string;
}

type HeaderSource =
  | Headers
  | Record<string, string>
  | ReadonlyArray<readonly [string, string]>;

interface StoreOptions {
  now?: () => number;
  random?: () => number;
}

function isIpLiteralHost(hostname: string): boolean {
  // IPv6 literals arrive bracketed from URL parsing of "http://[::1]/".
  if (hostname.startsWith("[") && hostname.endsWith("]")) return true;
  if (hostname.includes(":")) return true; // unbracketed IPv6, defensive
  // IPv4 (including shorthand/integer forms a URL parser may leave behind).
  if (/^\d+$/.test(hostname)) return true;
  if (/^(\d{1,3}\.){1,3}\d{1,3}$/.test(hostname)) return true;
  if (/^0x[0-9a-f]+$/i.test(hostname)) return true;
  return false;
}

function getHeader(headers: HeaderSource, name: string): string | null {
  const lower = name.toLowerCase();
  if (typeof (headers as Headers).get === "function") {
    return (headers as Headers).get(name);
  }
  if (Array.isArray(headers)) {
    for (const [key, value] of headers) {
      if (key.toLowerCase() === lower) return value;
    }
    return null;
  }
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === lower) return value;
  }
  return null;
}

function parseReportToGroups(raw: string): ReportToGroup[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return [];
  }
  const groups: ReportToGroup[] = [];
  for (const [name, value] of Object.entries(parsed as Record<string, unknown>)) {
    if (typeof value !== "object" || value === null) continue;
    const group = value as Record<string, unknown>;
    const maxAge = group.max_age;
    const endpoints = group.endpoints;
    if (typeof maxAge !== "number" || !Number.isFinite(maxAge) || maxAge < 0) {
      continue;
    }
    if (!Array.isArray(endpoints)) continue;
    const parsedEndpoints: ReportToEndpoint[] = [];
    for (const endpoint of endpoints) {
      if (
        typeof endpoint === "object" &&
        endpoint !== null &&
        typeof (endpoint as Record<string, unknown>).url === "string"
      ) {
        parsedEndpoints.push({
          url: (endpoint as Record<string, unknown>).url as string,
        });
      }
    }
    if (parsedEndpoints.length === 0) continue;
    groups.push({ name, maxAge, endpoints: parsedEndpoints });
  }
  return groups;
}

function parseFraction(value: unknown): number | null | "invalid" {
  if (value === undefined) return null;
  if (typeof value !== "number" || !Number.isFinite(value)) return "invalid";
  if (value < 0 || value > 1) return "invalid";
  return value;
}

export class NelPolicyStore {
  private readonly policies = new Map<string, NelPolicy>();
  private readonly now: () => number;
  private readonly random: () => number;

  constructor(options: StoreOptions = {}) {
    this.now = options.now ?? (() => Date.now());
    this.random = options.random ?? (() => Math.random());
  }

  /**
   * Process a response's headers. Only HTTPS responses from non-IP-literal
   * hosts may install or remove NEL policies.
   */
  processResponseHeaders(responseUrl: string, headers: HeaderSource): void {
    let url: URL;
    try {
      url = new URL(responseUrl);
    } catch {
      return;
    }
    if (url.protocol !== "https:") return; // cleartext NEL headers are ignored
    if (isIpLiteralHost(url.hostname)) return;

    const nelHeader = getHeader(headers, "NEL");
    if (nelHeader === null) return;

    const origin = url.origin;

    let parsed: unknown;
    try {
      parsed = JSON.parse(nelHeader);
    } catch {
      return;
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      return;
    }
    const nel = parsed as Record<string, unknown>;

    const maxAge = nel.max_age;
    if (
      typeof maxAge !== "number" ||
      !Number.isInteger(maxAge) ||
      maxAge < 0
    ) {
      return;
    }

    // max_age of 0 removes any existing policy for this origin.
    if (maxAge === 0) {
      this.policies.delete(origin);
      return;
    }

    const reportTo = nel.report_to;
    if (typeof reportTo !== "string" || reportTo.length === 0) return;

    // report_to must name a group declared in THIS response's Report-To header.
    const reportToHeader = getHeader(headers, "Report-To");
    if (reportToHeader === null) return;
    const group = parseReportToGroups(reportToHeader).find(
      (candidate) => candidate.name === reportTo,
    );
    if (!group) return;

    const successFraction = parseFraction(nel.success_fraction);
    const failureFraction = parseFraction(nel.failure_fraction);
    if (successFraction === "invalid" || failureFraction === "invalid") {
      return; // out-of-range fractions invalidate the entire policy
    }

    const includeSubdomains = nel.include_subdomains === true;

    this.policies.set(origin, {
      origin,
      reportToGroup: group,
      includeSubdomains,
      successFraction: successFraction ?? 1.0,
      failureFraction: failureFraction ?? 1.0,
      expiresAt: this.now() + maxAge * 1000,
    });
  }

  /** Policy that applies to this exact origin, if any. */
  getPolicy(origin: string): NelPolicy | undefined {
    const policy = this.policies.get(origin);
    if (!policy) return undefined;
    if (policy.expiresAt <= this.now()) {
      this.policies.delete(origin);
      return undefined;
    }
    return policy;
  }

  /**
   * Resolve the policy governing a request URL. Exact-origin policies win;
   * otherwise an ancestor-domain policy with include_subdomains may match.
   * Returns whether the match came via a subdomain ancestor.
   */
  private matchPolicy(
    requestUrl: URL,
  ): { policy: NelPolicy; viaSubdomain: boolean } | null {
    const exact = this.getPolicy(requestUrl.origin);
    if (exact) return { policy: exact, viaSubdomain: false };

    if (requestUrl.protocol !== "https:") return null;
    if (isIpLiteralHost(requestUrl.hostname)) return null;

    const host = requestUrl.hostname;
    for (const policy of this.policies.values()) {
      if (policy.expiresAt <= this.now()) continue;
      if (!policy.includeSubdomains) continue;
      let policyUrl: URL;
      try {
        policyUrl = new URL(policy.origin);
      } catch {
        continue;
      }
      if (policyUrl.protocol !== requestUrl.protocol) continue;
      const policyHost = policyUrl.hostname;
      if (host !== policyHost && host.endsWith("." + policyHost)) {
        return { policy, viaSubdomain: true };
      }
    }
    return null;
  }

  /**
   * Build a NEL report for a completed/failed request, or null if no policy
   * applies or sampling drops it. Subdomain-matched reports carry only the
   * request's origin in `url` (per this task's rule).
   */
  generateReport(info: NelRequestInfo): NelReport | null {
    let requestUrl: URL;
    try {
      requestUrl = new URL(info.url);
    } catch {
      return null;
    }
    const match = this.matchPolicy(requestUrl);
    if (!match) return null;

    const { policy, viaSubdomain } = match;
    const phase = info.errorType ? "error" : "ok";
    const fraction =
      phase === "error" ? policy.failureFraction : policy.successFraction;
    if (fraction <= 0) return null;
    if (fraction < 1 && this.random() >= fraction) return null;

    return {
      type: "network-error",
      url: viaSubdomain ? requestUrl.origin : info.url,
      referrer: info.referrer ?? "",
      samplingFraction: fraction,
      serverIp: info.serverIp ?? "",
      protocol: info.protocol ?? "",
      method: info.method ?? "GET",
      statusCode: info.statusCode ?? 0,
      elapsedTime: info.elapsedTime ?? 0,
      phase,
      errorType: info.errorType ?? null,
      deliveryEndpoints: policy.reportToGroup.endpoints.map(
        (endpoint) => endpoint.url,
      ),
    };
  }
}
