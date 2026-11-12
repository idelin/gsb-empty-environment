import type { OriginRelationship, StructuredOrigin } from "./types.ts";

/**
 * 最小多标签公共后缀表（覆盖测试与常见场景；宿主可扩展）。
 * 真实浏览器使用完整 PSL，这里保持确定性、零依赖。
 */
const MULTI_PART_SUFFIXES: ReadonlySet<string> = new Set([
  "co.uk",
  "org.uk",
  "ac.uk",
  "gov.uk",
  "co.jp",
  "co.kr",
  "com.cn",
  "org.cn",
  "com.au",
]);

/** 从 URL 解析结构化来源；非法 URL 返回 null。 */
export function parseOrigin(url: string): StructuredOrigin | null {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return null;
  }
  if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
    return null;
  }
  const port = parsed.port === "" ? null : Number(parsed.port);
  return {
    scheme: parsed.protocol === "https:" ? "https" : "http",
    host: parsed.hostname.toLowerCase(),
    port,
  };
}

function originEqual(a: StructuredOrigin, b: StructuredOrigin): boolean {
  return a.scheme === b.scheme && a.host === b.host && a.port === b.port;
}

/** 计算主机的可注册域（eTLD+1 的确定性近似）。 */
function registrableDomain(host: string): string {
  const labels = host.split(".");
  if (labels.length <= 2) {
    return host;
  }
  const lastTwo = labels.slice(-2).join(".");
  if (MULTI_PART_SUFFIXES.has(lastTwo)) {
    return labels.slice(-3).join(".");
  }
  return lastTwo;
}

function siteEqual(a: StructuredOrigin, b: StructuredOrigin): boolean {
  return (
    a.scheme === b.scheme &&
    registrableDomain(a.host) === registrableDomain(b.host)
  );
}

/** 计算“文档来源 → 资源来源”的有限来源关系。 */
export function classifyRelationship(
  documentOrigin: StructuredOrigin,
  resourceOrigin: StructuredOrigin,
): OriginRelationship {
  if (originEqual(documentOrigin, resourceOrigin)) {
    return "same-origin";
  }
  if (siteEqual(documentOrigin, resourceOrigin)) {
    return "same-site";
  }
  return "cross-site";
}

/** 来源的稳定字符串形式（用于缓存键序列化）。 */
export function serializeOrigin(origin: StructuredOrigin): string {
  const port = origin.port === null ? "" : `:${origin.port}`;
  return `${origin.scheme}://${origin.host}${port}`;
}
