export type AsType = "script" | "style" | "image" | "font";

export type CrossOriginMode = "anonymous" | "use-credentials";

export interface PreloadHint {
  url: string;
  as: AsType;
  crossorigin?: CrossOriginMode;
  integrity?: string;
  /** 收到 103 提示的时间戳（毫秒），默认由 addHint 填充 */
  receivedAt?: number;
}

export interface TagRequest {
  url: string;
  as: AsType;
  crossorigin?: CrossOriginMode;
  integrity?: string;
}

export interface FinalResponseInfo {
  status: number;
  /** 终态响应的 Content-Security-Policy 头（可省略，表示无 CSP） */
  csp?: string;
  /** 终态是否经历了重定向 */
  redirected?: boolean;
}

export type RejectReason =
  | "no-entry"
  | "expired"
  | "no-final-response"
  | "csp-blocked"
  | "crossorigin-mismatch"
  | "integrity-mismatch";

export type ReuseDecision =
  | { reuse: true }
  | { reuse: false; reason: RejectReason };

const TTL_MS = 30_000;

const AS_TO_DIRECTIVE: Record<AsType, string> = {
  script: "script-src",
  style: "style-src",
  image: "img-src",
  font: "font-src",
};

interface CspPolicy {
  directives: Map<string, string[]>;
}

function parseCsp(header: string): CspPolicy {
  const directives = new Map<string, string[]>();
  for (const part of header.split(";")) {
    const tokens = part.trim().split(/\s+/).filter(Boolean);
    if (tokens.length === 0) continue;
    const name = tokens[0].toLowerCase();
    if (!directives.has(name)) directives.set(name, tokens.slice(1));
  }
  return { directives };
}

function hostMatches(patternHost: string, urlHost: string): boolean {
  if (patternHost.startsWith("*.")) {
    const suffix = patternHost.slice(1); // ".example.com"
    return urlHost === patternHost.slice(2) || urlHost.endsWith(suffix);
  }
  return patternHost === urlHost;
}

function sourceAllows(source: string, url: URL, pageOrigin: string): boolean {
  const lower = source.toLowerCase();
  if (lower === "'none'") return false;
  if (lower === "*") return url.protocol === "http:" || url.protocol === "https:";
  if (lower === "'self'") return url.origin === pageOrigin;
  if (lower.startsWith("'")) return false; // 关键字源与 preload 复用判定无关
  if (lower === "data:" || lower === "blob:") return false;
  // scheme 源，如 https:
  if (/^[a-z][a-z0-9+.-]*:$/i.test(lower)) {
    return url.protocol === lower;
  }
  // host 源，如 example.com / https://example.com / *.example.com[:443][/path]
  let rest = source;
  const schemeMatch = /^([a-z][a-z0-9+.-]*):\/\//i.exec(rest);
  if (schemeMatch) {
    if (url.protocol !== schemeMatch[1].toLowerCase() + ":") return false;
    rest = rest.slice(schemeMatch[0].length);
  }
  const hostPort = rest.split("/")[0];
  const [hostPattern, portPattern] = hostPort.split(":");
  if (!hostMatches(hostPattern.toLowerCase(), url.hostname.toLowerCase())) return false;
  if (portPattern) {
    const port = portPattern === "*"
      ? url.port || (url.protocol === "https:" ? "443" : "80")
      : url.port;
    if (port !== portPattern) return false;
  }
  return true;
}

function cspAllows(policy: CspPolicy, as: AsType, rawUrl: string, pageOrigin: string): boolean {
  const directive = AS_TO_DIRECTIVE[as];
  const sources = policy.directives.get(directive) ?? policy.directives.get("default-src");
  if (!sources) return true; // 无对应指令，CSP 不限制
  if (sources.includes("'none'")) return false;
  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    return false;
  }
  return sources.some((s) => sourceAllows(s, url, pageOrigin));
}

export interface PreloadReuseCheckerOptions {
  /** 页面源，用于 CSP 中 'self' 的判定 */
  pageOrigin: string;
  /** 当前时间（毫秒），便于测试注入 */
  now?: () => number;
  /** 条目有效期，默认 30000ms */
  ttlMs?: number;
}

interface StoredHint {
  url: string;
  as: AsType;
  crossorigin?: CrossOriginMode;
  integrity?: string;
  receivedAt: number;
}

export class PreloadReuseChecker {
  private hints = new Map<string, StoredHint>();
  private csp: CspPolicy | null | undefined; // undefined = 终态未到，null = 无 CSP 头
  private readonly pageOrigin: string;
  private readonly now: () => number;
  private readonly ttlMs: number;

  constructor(options: PreloadReuseCheckerOptions) {
    this.pageOrigin = options.pageOrigin;
    this.now = options.now ?? (() => Date.now());
    this.ttlMs = options.ttlMs ?? TTL_MS;
  }

  private key(url: string, as: AsType): string {
    return `${as} ${url}`;
  }

  /** 记录 103 Early Hints 中 Link: rel=preload 的条目 */
  addHint(hint: PreloadHint): void {
    this.hints.set(this.key(hint.url, hint.as), {
      url: hint.url,
      as: hint.as,
      crossorigin: hint.crossorigin,
      integrity: hint.integrity,
      receivedAt: hint.receivedAt ?? this.now(),
    });
  }

  /** 终态响应到达：重定向或非 2xx 清空全部条目，否则记录终态 CSP */
  handleFinalResponse(info: FinalResponseInfo): void {
    if (info.redirected || info.status < 200 || info.status >= 300) {
      this.hints.clear();
      this.csp = undefined;
      return;
    }
    this.csp = info.csp ? parseCsp(info.csp) : null;
  }

  /** 丢弃过期条目 */
  sweep(): void {
    const now = this.now();
    for (const [key, hint] of this.hints) {
      if (now - hint.receivedAt > this.ttlMs) this.hints.delete(key);
    }
  }

  /**
   * 判定页面标签请求能否复用提前抓取的内容。
   * 可复用时消费该条目；不可复用时丢弃该条目（调用方应重新抓取）。
   */
  decide(tag: TagRequest): ReuseDecision {
    const key = this.key(tag.url, tag.as);
    const hint = this.hints.get(key);
    if (!hint) {
      this.sweep();
      return { reuse: false, reason: "no-entry" };
    }
    if (this.now() - hint.receivedAt > this.ttlMs) {
      this.hints.delete(key);
      return { reuse: false, reason: "expired" };
    }
    if (this.csp === undefined) return { reuse: false, reason: "no-final-response" };
    if (this.csp !== null && !cspAllows(this.csp, tag.as, tag.url, this.pageOrigin)) {
      this.hints.delete(key);
      return { reuse: false, reason: "csp-blocked" };
    }
    if ((hint.crossorigin ?? undefined) !== (tag.crossorigin ?? undefined)) {
      this.hints.delete(key);
      return { reuse: false, reason: "crossorigin-mismatch" };
    }
    if ((hint.integrity ?? undefined) !== (tag.integrity ?? undefined)) {
      this.hints.delete(key);
      return { reuse: false, reason: "integrity-mismatch" };
    }
    this.hints.delete(key);
    return { reuse: true };
  }

  get size(): number {
    this.sweep();
    return this.hints.size;
  }
}
