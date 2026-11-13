import { isPublicIp, parseIpLiteral } from "./ip.ts";

/**
 * 浏览器无关的资源类型。
 * Playwright/Puppeteer 的具体字符串由 adapters 层归一化。
 */
export type ResourceType =
  | "document"
  | "stylesheet"
  | "image"
  | "media"
  | "font"
  | "script"
  | "xhr"
  | "fetch"
  | "websocket"
  | "eventsource"
  | "other";

export type RequestInfo = {
  /** 当前请求 URL（含 data:/blob: 等）。 */
  url: string;
  type: ResourceType;
  /** 发起者 frame 的 URL；主页面导航为 null。 */
  initiatorFrameUrl: string | null;
  /**
   * 已发生的重定向链，按时间顺序排列，不含当前 URL。
   * 元素可以是字符串或带 url 的对象，方便直接传 Playwright/Puppeteer 结构。
   */
  redirectChain?: ReadonlyArray<string | { url: string }>;
};

/**
 * 调用方提供的主机解析回调：把主机名解析为全部已解析 IP 字面量。
 * IP 字面量主机不会调用该回调；抛错或返回空均按失败关闭处理。
 */
export type AddressResolver = (hostname: string) => Promise<readonly string[]>;

export type PolicyOptions = {
  /** 调用方的主机地址解析回调。 */
  resolveAddresses: AddressResolver;
  /** 主页面允许的主机白名单（主机名小写，支持 ".example.com" 子域通配）。 */
  allowedHosts: readonly string[];
  /** 重定向链与当前请求合计的最大跳数，默认 5。 */
  maxRedirects?: number;
};

export type DenyReason =
  | "websocket-denied"
  | "eventsource-denied"
  | "invalid-url"
  | "scheme-denied"
  | "data-type-denied"
  | "blob-cross-origin"
  | "hostname-not-allowed"
  | "private-address"
  | "resolve-failed"
  | "too-many-redirects";

export type Decision =
  | { allowed: true }
  | {
      allowed: false;
      reason: DenyReason;
      /** 触发拒绝的具体 URL（重定向场景下为问题跳）。 */
      url: string;
      detail?: string;
    };

export type RequestPolicy = {
  decide(request: RequestInfo): Promise<Decision>;
};

const IMAGE_FONT_TYPES: ReadonlySet<ResourceType> = new Set(["image", "font"]);

function normalizeHost(hostname: string): string {
  const trimmed = hostname.toLowerCase().trim();
  return trimmed.endsWith(".") && trimmed.length > 1
    ? trimmed.slice(0, -1)
    : trimmed;
}

function hostMatches(hostname: string, pattern: string): boolean {
  const host = normalizeHost(hostname);
  const rule = normalizeHost(pattern);
  if (rule.startsWith(".")) {
    const parent = rule.slice(1);
    return host === parent || host.endsWith(`.${parent}`);
  }
  return host === rule;
}

function isAllowedHost(hostname: string, allowedHosts: readonly string[]): boolean {
  return allowedHosts.some((pattern) => hostMatches(hostname, pattern));
}

function originOf(url: URL): string | undefined {
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return undefined;
  }
  return url.origin;
}

function dataMime(url: URL): string {
  // WHATWG URL 会把 data: 后的内容放进 pathname
  const body = url.pathname;
  const metadata = body.split(",", 1)[0] ?? "";
  const mimePart = metadata.split(";")[0]?.trim() ?? "";
  if (mimePart.length === 0) {
    return "text/plain";
  }
  return mimePart.toLowerCase();
}

function isImageOrFontMime(mime: string): boolean {
  return mime.startsWith("image/") || mime.startsWith("font/");
}

type SchemeVerdict =
  | { ok: true; url: URL }
  | { ok: false; reason: DenyReason; detail?: string };

function parseAndCheckScheme(
  rawUrl: string,
  type: ResourceType,
  initiatorFrameUrl: string | null,
): SchemeVerdict {
  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    return { ok: false, reason: "invalid-url", detail: "URL 无法解析" };
  }

  if (type === "websocket") {
    return { ok: false, reason: "websocket-denied" };
  }
  if (type === "eventsource") {
    return { ok: false, reason: "eventsource-denied" };
  }

  const protocol = url.protocol.toLowerCase();
  if (protocol === "http:" || protocol === "https:") {
    if (url.username !== "" || url.password !== "") {
      return { ok: false, reason: "invalid-url", detail: "不允许 URL 内嵌凭据" };
    }
    return { ok: true, url };
  }

  if (protocol === "data:") {
    if (!IMAGE_FONT_TYPES.has(type) || !isImageOrFontMime(dataMime(url))) {
      return { ok: false, reason: "data-type-denied", detail: "仅允许图片/字体 data URI" };
    }
    return { ok: true, url };
  }

  if (protocol === "blob:") {
    if (initiatorFrameUrl === null) {
      return { ok: false, reason: "blob-cross-origin", detail: "blob 无页面发起者" };
    }
    let initiator: URL;
    try {
      initiator = new URL(initiatorFrameUrl);
    } catch {
      return { ok: false, reason: "blob-cross-origin", detail: "发起者 URL 无效" };
    }
    const initiatorOrigin = originOf(initiator);
    if (!initiatorOrigin || url.origin !== initiatorOrigin) {
      return { ok: false, reason: "blob-cross-origin", detail: "仅允许本页创建的同源 blob" };
    }
    return { ok: true, url };
  }

  // file:、chrome:、view-source:、ftp:、ws:、wss: 及其他一切协议
  return { ok: false, reason: "scheme-denied", detail: `协议 ${protocol} 不允许` };
}

export function createRequestPolicy(options: PolicyOptions): RequestPolicy {
  const maxHops = options.maxRedirects ?? 5;
  const cache = new Map<string, Decision>();

  async function hostIsPublic(hostname: string): Promise<Decision | undefined> {
    const host = normalizeHost(hostname);
    const literal = parseIpLiteral(host);
    if (literal) {
      return isPublicIp(literal)
        ? undefined
        : {
            allowed: false,
            reason: "private-address",
            url: "",
            detail: `IP 字面量 ${host} 非公网地址`,
          };
    }

    const cacheKey = `dns:${host}`;
    const cached = cache.get(cacheKey);
    if (cached) {
      return cached.allowed ? undefined : cached;
    }

    let addresses: readonly string[];
    try {
      addresses = await options.resolveAddresses(host);
    } catch (error) {
      const decision: Decision = {
        allowed: false,
        reason: "resolve-failed",
        url: "",
        detail: error instanceof Error ? error.message : String(error),
      };
      cache.set(cacheKey, decision);
      return decision;
    }

    if (addresses.length === 0) {
      const decision: Decision = {
        allowed: false,
        reason: "resolve-failed",
        url: "",
        detail: `主机 ${host} 未解析出任何地址`,
      };
      cache.set(cacheKey, decision);
      return decision;
    }

    for (const address of addresses) {
      const parsed = parseIpLiteral(address);
      if (!parsed || !isPublicIp(parsed)) {
        const decision: Decision = {
          allowed: false,
          reason: "private-address",
          url: "",
          detail: `主机 ${host} 解析到非公网地址 ${address}`,
        };
        cache.set(cacheKey, decision);
        return decision;
      }
    }

    cache.set(cacheKey, { allowed: true });
    return undefined;
  }

  async function evaluateHop(
    rawUrl: string,
    type: ResourceType,
    initiatorFrameUrl: string | null,
    isMainFrame: boolean,
  ): Promise<Decision> {
    const scheme = parseAndCheckScheme(rawUrl, type, initiatorFrameUrl);
    if (!scheme.ok) {
      return { allowed: false, reason: scheme.reason, url: rawUrl, detail: scheme.detail };
    }

    const { url } = scheme;
    if (url.protocol === "data:" || url.protocol === "blob:") {
      return { allowed: true };
    }

    const hostDenial = await hostIsPublic(url.hostname);
    if (hostDenial && !hostDenial.allowed) {
      return { ...hostDenial, url: rawUrl };
    }
    if (isMainFrame && !isAllowedHost(url.hostname, options.allowedHosts)) {
      return {
        allowed: false,
        reason: "hostname-not-allowed",
        url: rawUrl,
        detail: `主页面主机 ${url.hostname} 不在白名单内`,
      };
    }
    return { allowed: true };
  }

  return {
    async decide(request: RequestInfo): Promise<Decision> {
      if (request.type === "websocket") {
        return { allowed: false, reason: "websocket-denied", url: request.url };
      }
      if (request.type === "eventsource") {
        return { allowed: false, reason: "eventsource-denied", url: request.url };
      }

      const chain = (request.redirectChain ?? []).map((entry) =>
        typeof entry === "string" ? entry : entry.url,
      );
      // 重定向链每一跳加当前请求，总跳数不超过 5
      if (chain.length + 1 > maxHops) {
        return {
          allowed: false,
          reason: "too-many-redirects",
          url: request.url,
          detail: `总跳数 ${chain.length + 1} 超过上限 ${maxHops}`,
        };
      }

      const isMainFrame = request.initiatorFrameUrl === null;
      for (const hopUrl of chain) {
        // 逐跳重放完整策略：协议、公网与主页面白名单都要重新过一遍
        const decision = await evaluateHop(hopUrl, "document", null, isMainFrame);
        if (!decision.allowed) {
          return decision;
        }
      }
      return evaluateHop(
        request.url,
        request.type,
        request.initiatorFrameUrl,
        isMainFrame,
      );
    },
  };
}
