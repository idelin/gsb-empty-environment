import { normalizeDomain, sameDomain } from "./domain.ts";
import { evaluateUrl, type ParsedHttpsUrl } from "./netsec.ts";

/**
 * 应答实体准入：校验 RDAP JSON 是否与“本次查询对象”一致。
 * 任何一项不符都返回 admitted=false，调用方必须整条丢弃且禁止写缓存。
 */

/** RDAP 对象类型。域名查询只接受 domain。 */
export type ObjectClassName =
  | "entity"
  | "nameserver"
  | "domain"
  | "ip network"
  | "autnum";

export interface RdapLink {
  rel?: string;
  href?: string;
  type?: string;
  [k: string]: unknown;
}

export interface RdapResponse {
  objectClassName?: unknown;
  ldhName?: unknown;
  rdapConformance?: unknown;
  links?: unknown;
  [k: string]: unknown;
}

export type AdmissionViolation =
  | "not-an-object"
  | "object-class-mismatch"
  | "ldh-name-missing"
  | "ldh-name-mismatch"
  | "conformance-missing"
  | "conformance-mismatch";

export interface AdmissionResult {
  admitted: boolean;
  violation?: AdmissionViolation;
}

export interface AdmissionPolicy {
  /** 期望的对象类型，域名查询固定 "domain"。 */
  expectedObjectClass: ObjectClassName;
  /**
   * rdapConformance 必须包含的级别标识。
   * 默认要求 "rdap_level_0"（RFC 9083）。
   */
  requiredConformance: string[];
}

export const DOMAIN_ADMISSION_POLICY: AdmissionPolicy = {
  expectedObjectClass: "domain",
  requiredConformance: ["rdap_level_0"],
};

/**
 * 校验应答实体。纯函数，不做网络、不做缓存。
 * @param queryName 发起查询时使用的域名（可含 Unicode / 大写 / 混合大小写 Punycode）
 */
export function admitResponse(
  body: unknown,
  queryName: string,
  policy: AdmissionPolicy = DOMAIN_ADMISSION_POLICY,
): AdmissionResult {
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    return { admitted: false, violation: "not-an-object" };
  }
  const obj = body as RdapResponse;

  if (obj.objectClassName !== policy.expectedObjectClass) {
    return { admitted: false, violation: "object-class-mismatch" };
  }

  if (typeof obj.ldhName !== "string" || obj.ldhName.length === 0) {
    return { admitted: false, violation: "ldh-name-missing" };
  }
  if (!sameDomain(obj.ldhName, queryName)) {
    return { admitted: false, violation: "ldh-name-mismatch" };
  }

  if (!Array.isArray(obj.rdapConformance) || obj.rdapConformance.length === 0) {
    return { admitted: false, violation: "conformance-missing" };
  }
  const present = new Set(
    obj.rdapConformance.filter((x): x is string => typeof x === "string"),
  );
  const missing = policy.requiredConformance.some((c) => !present.has(c));
  if (missing) {
    return { admitted: false, violation: "conformance-mismatch" };
  }

  return { admitted: true };
}

export interface SafeLink {
  rel: string;
  href: string;
  parsed: ParsedHttpsUrl;
}

/**
 * 从应答 links 中筛出“可安全跟随”的链接：
 * 仅 https、主机在引导服务器集合或调用方白名单内、非私有地址。
 * 不安全的链接被静默剔除（不抛异常），绝不返回给查询流程去请求。
 */
export function filterFollowableLinks(
  links: unknown,
  allowedHosts: ReadonlySet<string>,
  base?: string | URL,
): SafeLink[] {
  if (!Array.isArray(links)) {
    return [];
  }
  const safe: SafeLink[] = [];
  for (const link of links as RdapLink[]) {
    if (typeof link !== "object" || link === null || typeof link.href !== "string") {
      continue;
    }
    const verdict = evaluateUrl(link.href, allowedHosts, base);
    if (verdict.ok && verdict.parsed) {
      safe.push({
        rel: typeof link.rel === "string" ? link.rel : "",
        href: verdict.parsed.url.toString(),
        parsed: verdict.parsed,
      });
    }
  }
  return safe;
}

/** 仅做一次域名归一化，便于查询流程拼 RDAP URL。 */
export function buildRdapUrl(baseUrl: URL, queryName: string): URL | null {
  const normalized = normalizeDomain(queryName);
  if (!normalized) {
    return null;
  }
  const base = baseUrl.toString().endsWith("/") ? baseUrl.toString() : `${baseUrl.toString()}/`;
  return new URL(`domain/${encodeURIComponent(normalized)}`, base);
}
