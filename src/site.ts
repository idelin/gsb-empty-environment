import type { Site } from "./types.ts";

/**
 * 常见多级后缀的最小集合；测试使用的常规 .com 域取 eTLD+1。
 * 真实宿主应接入完整的公共后缀列表。
 */
const MULTI_PART_SUFFIXES = new Set([
  "co.uk",
  "org.uk",
  "ac.uk",
  "co.jp",
  "co.kr",
  "com.au",
  "com.cn",
  "co.nz",
]);

/**
 * 从 origin 提取站点（scheme + 注册域）。
 * 同站判定依据：scheme 与 eTLD+1 一致。
 */
export function siteOf(origin: string): Site {
  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    throw new TypeError(`invalid origin: ${origin}`);
  }
  const scheme = url.protocol.replace(":", "");
  const hostname = url.hostname.toLowerCase();
  const labels = hostname.split(".");

  let registrable: string;
  if (labels.length <= 2) {
    registrable = hostname;
  } else {
    const lastTwo = labels.slice(-2).join(".");
    const lastThree = labels.slice(-3).join(".");
    registrable = MULTI_PART_SUFFIXES.has(lastTwo) && labels.length >= 3
      ? lastThree
      : lastTwo;
  }

  return `${scheme}://${registrable}` as Site;
}

export function sameSite(a: string, b: string): boolean {
  return siteOf(a) === siteOf(b);
}

export function grantKey(
  embeddedOrigin: string,
  topLevelOrigin: string,
): { embeddedSite: Site; topLevelSite: Site } {
  return {
    embeddedSite: siteOf(embeddedOrigin),
    topLevelSite: siteOf(topLevelOrigin),
  };
}
