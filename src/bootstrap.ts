import { parseHttpsUrl } from "./netsec.ts";

/**
 * IANA RDAP 引导文件（https://data.iana.org/rdap/dns.json）的最小类型。
 * 结构：{
 *   "version": "...",
 *   "publication": "2024-...",
 *   "description": [...],
 *   "services": [ [ ["tld1","tld2"], ["https://rdap.example/"] ], ... ]
 * }
 */

export interface DnsBootstrap {
  version?: string;
  publication?: string;
  description?: string[];
  services: unknown;
}

export interface BootstrapEntry {
  /** 大写、去掉前导点的后缀标签，如 "COM"；空数组项表示根/兜底（"*"）。 */
  tlds: string[];
  /** 引导文件里声明的、已通过 https 校验的服务基址。 */
  baseUrls: URL[];
  /** 从服务基址派生出的主机准入集合（小写）。 */
  hosts: Set<string>;
}

export const IANA_DNS_BOOTSTRAP_URL = "https://data.iana.org/rdap/dns.json";

function normalizeTld(label: unknown): string | null {
  if (typeof label !== "string" || label.length === 0) {
    return null;
  }
  const trimmed = label.trim().replace(/^\./, "").replace(/\.$/, "");
  return trimmed.length === 0 ? null : trimmed.toUpperCase();
}

/**
 * 解析并校验引导文件。任何服务项缺 https 基址、结构畸形都会抛出，
 * 避免把不可信/不完整的引导内容当作准入依据。
 */
export function parseBootstrap(raw: DnsBootstrap): BootstrapEntry[] {
  if (!raw || typeof raw !== "object" || !Array.isArray(raw.services)) {
    throw new Error("bootstrap: missing services array");
  }
  const entries: BootstrapEntry[] = [];
  raw.services.forEach((service, index) => {
    if (!Array.isArray(service) || service.length < 2 ||
      !Array.isArray(service[0]) || !Array.isArray(service[1])) {
      throw new Error(`bootstrap: malformed service entry #${index}`);
    }
    const tlds = (service[0] as unknown[])
      .map(normalizeTld)
      .filter((x): x is string => x !== null);
    const baseUrls: URL[] = [];
    const hosts = new Set<string>();
    for (const candidate of service[1] as unknown[]) {
      if (typeof candidate !== "string") {
        throw new Error(`bootstrap: service #${index} has non-string base url`);
      }
      const parsed = parseHttpsUrl(candidate);
      if (!parsed) {
        throw new Error(`bootstrap: service #${index} base is not https: ${candidate}`);
      }
      baseUrls.push(parsed.url);
      hosts.add(parsed.host);
    }
    if (baseUrls.length === 0) {
      throw new Error(`bootstrap: service #${index} has no https base url`);
    }
    entries.push({ tlds, baseUrls, hosts });
  });
  return entries;
}

/** 取域名最右侧标签作为引导查找键（已大写）。 */
export function tldOf(domain: string): string {
  const cleaned = domain.trim().replace(/\.$/, "");
  const dot = cleaned.lastIndexOf(".");
  return (dot === -1 ? cleaned : cleaned.slice(dot + 1)).toUpperCase();
}

export interface BootstrapMatch {
  entry: BootstrapEntry;
  /** 首个 https 基址（去掉查询串，保留路径前缀）。 */
  baseUrl: URL;
}

export class BootstrapRegistry {
  private entries: BootstrapEntry[] = [];
  private publication: string | undefined;
  private fetchedAt: Date | null = null;

 /**
  * 用一份已拉取的引导 JSON 重建注册表（离线/测试可直接注入）。
  */
  load(raw: DnsBootstrap, now: Date = new Date()): void {
    this.entries = parseBootstrap(raw);
    this.publication = typeof raw.publication === "string" ? raw.publication : undefined;
    this.fetchedAt = now;
  }

  /** 直接从 IANA 拉取“当前内容”。fetch 可注入以便测试与代理定制。 */
  async refresh(
    fetchImpl: typeof fetch = fetch,
    url: string = IANA_DNS_BOOTSTRAP_URL,
  ): Promise<void> {
    const response = await fetchImpl(url);
    if (!response.ok) {
      throw new Error(`bootstrap: fetch failed ${response.status} ${response.statusText}`);
    }
    const raw = (await response.json()) as DnsBootstrap;
    this.load(raw);
  }

  get isLoaded(): boolean {
    return this.entries.length > 0;
  }

  get currentPublication(): string | undefined {
    return this.publication;
  }

  get loadedAt(): Date | null {
    return this.fetchedAt;
  }

  /** 所有引导服务器主机的并集，供 links 准入使用。 */
  allBootstrapHosts(): Set<string> {
    const all = new Set<string>();
    for (const entry of this.entries) {
      for (const host of entry.hosts) {
        all.add(host);
      }
    }
    return all;
  }

  /**
   * 按 TLD 找引导服务。引导缺项（该 TLD 不在文件里）返回 null，
   * 调用方必须据此终止查询，而不是退化为任意服务器。
   */
  match(domain: string): BootstrapMatch | null {
    if (!this.isLoaded) {
      throw new Error("bootstrap: registry not loaded");
    }
    const tld = tldOf(domain);
    for (const entry of this.entries) {
      if (entry.tlds.includes(tld)) {
        return { entry, baseUrl: entry.baseUrls[0] };
      }
    }
    return null;
  }
}
