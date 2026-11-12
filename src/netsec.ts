import { isIP } from "node:net";

/**
 * 网络出口安全原语：只允许 https、主机名落在准入集合内、且永远不发往
 * 私有/环回/链路本地/保留地址。供 links 跟随与 3xx 重定向共用。
 */

export interface ParsedHttpsUrl {
  /** 小写、去掉末尾根点的主机名（IP 字面量保持原样）。 */
  host: string;
  /** 4 表示 IPv4，6 表示 IPv6，0 表示普通 DNS 主机名。 */
  ipVersion: 4 | 6 | 0;
  url: URL;
}

/**
 * 解析并强制 https。非 http(s) scheme、相对地址、畸形 URL 一律拒绝。
 */
export function parseHttpsUrl(input: string, base?: string | URL): ParsedHttpsUrl | null {
  let url: URL;
  try {
    url = new URL(input, base);
  } catch {
    return null;
  }
  if (url.protocol !== "https:") {
    return null;
  }
  // URL 对 https 默认端口 443，端口不参与主机准入（端口仍原样保留在 url 上）。
  const rawHost = url.hostname;
  if (!rawHost) {
    return null;
  }
  // IPv6 字面量在 URL 里带方括号，剥掉再判断。
  const host = rawHost.startsWith("[") && rawHost.endsWith("]")
    ? rawHost.slice(1, -1)
    : rawHost;
  const kind = isIP(host);
  return {
    host: kind === 0 ? host.toLowerCase().replace(/\.$/, "") : host,
    ipVersion: kind === 4 ? 4 : kind === 6 ? 6 : 0,
    url,
  };
}

function ipv4AsInt(host: string): number | null {
  if (isIP(host) !== 4) {
    return null;
  }
  const parts = host.split(".").map((p) => Number(p));
  // isIP 已保证四段 0-255，这里仅做拼装。
  return ((parts[0] << 24) >>> 0) +
    ((parts[1] << 16) >>> 0) +
    ((parts[2] << 8) >>> 0) +
    parts[3];
}

function inV4Prefix(value: number, prefix: string, bits: number): boolean {
  const base = ipv4AsInt(prefix);
  if (base === null) {
    return false;
  }
  const mask = bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0;
  return (value & mask) === (base & mask);
}

/**
 * 判断 IP 字面量是否属于“禁止外发”的非全局地址。
 * 覆盖 RFC1918、环回、链路本地、CGNAT、保留/未指定/多播，以及 IPv6
 * 的 ::、::1、fe80::/10、fc00::/7、多播与 IPv4 映射地址里的私有段。
 */
export function isPrivateOrNonGlobal(host: string): boolean {
  const kind = isIP(host);
  if (kind === 4) {
    const value = ipv4AsInt(host);
    if (value === null) {
      return true; // 无法解析按不可外发处理（fail-closed）。
    }
    const blocked: Array<[string, number]> = [
      ["0.0.0.0", 8], // "本网络"
      ["10.0.0.0", 8], // RFC1918
      ["100.64.0.0", 10], // CGNAT
      ["127.0.0.0", 8], // 环回
      ["169.254.0.0", 16], // 链路本地（含云元数据 169.254.169.254）
      ["172.16.0.0", 12], // RFC1918
      ["192.0.0.0", 24], // IETF 协议保留
      ["192.0.2.0", 24], // TEST-NET-1
      ["192.88.99.0", 24], // 6to4 中继任播（已停用）
      ["192.168.0.0", 16], // RFC1918
      ["198.18.0.0", 15], // 基准测试
      ["198.51.100.0", 24], // TEST-NET-2
      ["203.0.113.0", 24], // TEST-NET-3
      ["224.0.0.0", 4], // 多播
      ["240.0.0.0", 4], // 保留（含 255.255.255.255）
    ];
    return blocked.some(([prefix, bits]) => inV4Prefix(value, prefix, bits));
  }
  if (kind === 6) {
    const addr = host.toLowerCase();
    // IPv4 映射/兼容地址：提取尾部 IPv4 后按 v4 规则判定。
    const mappedV4 = /(?:^|:)(?:ffff:)?(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})$/i.exec(addr);
    if (mappedV4 && isIP(mappedV4[1]) === 4) {
      return isPrivateOrNonGlobal(mappedV4[1]);
    }
    if (addr === "::" || addr === "::1") {
      return true; // 未指定 / 环回
    }
    if (addr.startsWith("fe80") || addr.startsWith("fe90") ||
      addr.startsWith("fea0") || addr.startsWith("feb0")) {
      return true; // fe80::/10 链路本地
    }
    if (addr.startsWith("fc") || addr.startsWith("fd")) {
      return true; // fc00::/7 唯一本地（ULA）
    }
    if (addr.startsWith("ff")) {
      return true; // 多播
    }
    // 2001:db8::/32 文档保留、2001::/23 中除全局单播外的保留段按禁止处理。
    if (addr.startsWith("2001:db8") || addr.startsWith("2001:0:") ||
      addr.startsWith("2001::")) {
      return true;
    }
    if (addr.startsWith("::ffff:")) {
      return true; // 兼容映射但未带点分十进制，保守拒绝
    }
    return false;
  }
  // 不是 IP 字面量：DNS 名本身不在这里判私，交由主机准入集合判定。
  return false;
}

/**
 * 主机准入：忽略大小写，必须精确命中引导服务器主机或调用方白名单。
 * 命中后若该主机是 IP 字面量，还须通过非全局地址检查。
 */
export function isAllowedHost(host: string, allowedHosts: ReadonlySet<string>): boolean {
  const normalized = host.toLowerCase().replace(/\.$/, "");
  if (!allowedHosts.has(normalized)) {
    return false;
  }
  if (isIP(normalized) !== 0 && isPrivateOrNonGlobal(normalized)) {
    return false;
  }
  return true;
}

export type RejectReason =
  | "not-https"
  | "host-not-allowed"
  | "private-address";

export interface UrlVerdict {
  ok: boolean;
  reason?: RejectReason;
  parsed?: ParsedHttpsUrl;
}

/**
 * links 跟随 / 重定向落点的统一准入判定。
 */
export function evaluateUrl(
  input: string,
  allowedHosts: ReadonlySet<string>,
  base?: string | URL,
): UrlVerdict {
  const parsed = parseHttpsUrl(input, base);
  if (!parsed) {
    return { ok: false, reason: "not-https" };
  }
  if (parsed.ipVersion !== 0 && isPrivateOrNonGlobal(parsed.host)) {
    return { ok: false, reason: "private-address", parsed };
  }
  if (!isAllowedHost(parsed.host, allowedHosts)) {
    return { ok: false, reason: "host-not-allowed", parsed };
  }
  return { ok: true, parsed };
}
