import type { DnsBootstrap } from "../index.ts";

/**
 * 离线 IANA dns.json 夹具：结构与 https://data.iana.org/rdap/dns.json 一致，
 * 仅使用公网示例 RDAP 服务器主机。
 */
export const bootstrapFixture: DnsBootstrap = {
  version: "2026-11-01-0000",
  publication: "2026-11-01T00:00:00Z",
  description: ["RDAP bootstrap fixture for offline tests"],
  services: [
    [
      ["com", "net"],
      ["https://rdap.verisign.com/com/v1/"],
    ],
    [
      ["org"],
      ["https://rdap.publicinterestregistry.org/rdap/"],
    ],
    [
      ["jp"],
      ["https://rdap.jprs.jp/"],
    ],
  ],
};

export const COM_BASE = "https://rdap.verisign.com/com/v1/";
export const ORG_BASE = "https://rdap.publicinterestregistry.org/rdap/";
export const JP_BASE = "https://rdap.jprs.jp/";

export function domainResponse(name: string, overrides: Record<string, unknown> = {}) {
  return {
    objectClassName: "domain",
    ldhName: name,
    rdapConformance: ["rdap_level_0"],
    links: [],
    ...overrides,
  };
}

export type Route = {
  status?: number;
  headers?: Record<string, string>;
  body?: unknown;
};

/** 以 URL 前缀匹配的内存 fetch，便于精确模拟重定向与恶意落点。 */
export function mockFetch(routes: Array<[string, Route]>): typeof fetch {
  return async (input: string | URL | Request) => {
    const target = typeof input === "string" ? input : input.toString();
    for (const [prefix, route] of routes) {
      if (target.startsWith(prefix)) {
        return new Response(
          route.body === undefined ? null : JSON.stringify(route.body),
          {
            status: route.status ?? 200,
            headers: route.headers ?? {},
          },
        );
      }
    }
    throw new Error(`mockFetch: no route for ${target}`);
  };
}
