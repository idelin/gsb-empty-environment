/**
 * Payment Handler 注册校验（浏览器宿主）。
 *
 * 安全不变量：
 * 1. 支付方式清单必须由方式 URL 所属源直接获取；重定向只允许同源，
 *    跨源重定向视为攻击并拒绝。
 * 2. handler 来源必须被清单的 default_applications 或 supported_origins 列出。
 * 3. 清单被撤销（获取失败或不再授权）后，已注册 handler 不再响应。
 */

export interface FetchResponseLike {
  readonly status: number;
  readonly ok: boolean;
  readonly headers: { get(name: string): string | null };
  json(): Promise<unknown>;
}

export type FetchLike = (
  input: string,
  init?: { redirect?: "manual" | "follow" | "error" },
) => Promise<FetchResponseLike>;

export interface PaymentMethodManifest {
  default_applications?: string[];
  supported_origins?: string[] | "*";
}

export class CrossOriginRedirectError extends Error {
  readonly methodUrl: string;
  readonly redirectTarget: string;

  constructor(methodUrl: string, redirectTarget: string) {
    super(
      `Refused cross-origin redirect while fetching payment method manifest: ` +
        `${methodUrl} -> ${redirectTarget}`,
    );
    this.name = "CrossOriginRedirectError";
    this.methodUrl = methodUrl;
    this.redirectTarget = redirectTarget;
  }
}

export class ManifestFetchError extends Error {
  readonly methodUrl: string;

  constructor(methodUrl: string, message: string) {
    super(`Failed to fetch payment method manifest for ${methodUrl}: ${message}`);
    this.name = "ManifestFetchError";
    this.methodUrl = methodUrl;
  }
}

export class ManifestParseError extends Error {
  readonly methodUrl: string;

  constructor(methodUrl: string) {
    super(`Payment method manifest at ${methodUrl} is not a valid manifest object`);
    this.name = "ManifestParseError";
    this.methodUrl = methodUrl;
  }
}

export class HandlerNotAuthorizedError extends Error {
  readonly methodUrl: string;
  readonly handlerOrigin: string;

  constructor(methodUrl: string, handlerOrigin: string) {
    super(
      `Payment handler origin ${handlerOrigin} is not authorized by the manifest ` +
        `of ${methodUrl} (missing from default_applications and supported_origins)`,
    );
    this.name = "HandlerNotAuthorizedError";
    this.methodUrl = methodUrl;
    this.handlerOrigin = handlerOrigin;
  }
}

const REDIRECT_STATUSES = new Set([301, 302, 303, 307, 308]);

const defaultFetcher: FetchLike = (input, init) =>
  fetch(input, { redirect: init?.redirect ?? "manual" });

function parseManifest(methodUrl: string, body: unknown): PaymentMethodManifest {
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    throw new ManifestParseError(methodUrl);
  }
  const raw = body as Record<string, unknown>;
  const manifest: PaymentMethodManifest = {};
  if (raw.default_applications !== undefined) {
    if (
      !Array.isArray(raw.default_applications) ||
      !raw.default_applications.every((entry) => typeof entry === "string")
    ) {
      throw new ManifestParseError(methodUrl);
    }
    manifest.default_applications = [...raw.default_applications];
  }
  if (raw.supported_origins !== undefined) {
    if (raw.supported_origins === "*") {
      manifest.supported_origins = "*";
    } else if (
      Array.isArray(raw.supported_origins) &&
      raw.supported_origins.every((entry) => typeof entry === "string")
    ) {
      manifest.supported_origins = [...raw.supported_origins];
    } else {
      throw new ManifestParseError(methodUrl);
    }
  }
  return manifest;
}

export function isHandlerAuthorized(
  manifest: PaymentMethodManifest,
  handlerOrigin: string,
): boolean {
  const supported = manifest.supported_origins;
  if (supported === "*") {
    return true;
  }
  if (Array.isArray(supported)) {
    for (const entry of supported) {
      try {
        if (new URL(entry).origin === handlerOrigin) {
          return true;
        }
      } catch {
        // 忽略清单中的非法条目。
      }
    }
  }
  for (const app of manifest.default_applications ?? []) {
    try {
      if (new URL(app).origin === handlerOrigin) {
        return true;
      }
    } catch {
      // 忽略清单中的非法条目。
    }
  }
  return false;
}

export class PaymentHandlerRegistry {
  readonly #fetcher: FetchLike;
  readonly #maxRedirects: number;
  readonly #manifests = new Map<string, PaymentMethodManifest>();
  readonly #handlers = new Map<string, Set<string>>();

  constructor(options: { fetcher?: FetchLike; maxRedirects?: number } = {}) {
    this.#fetcher = options.fetcher ?? defaultFetcher;
    this.#maxRedirects = options.maxRedirects ?? 5;
  }

  /**
   * 从方式 URL 所属源直接获取清单。仅跟随同源重定向；
   * 遇到跨源重定向立即拒绝，防止方式 URL 被劫持到他源清单。
   */
  async fetchManifest(methodUrl: string): Promise<PaymentMethodManifest> {
    const methodOrigin = new URL(methodUrl).origin;
    let current = methodUrl;
    for (let hop = 0; hop <= this.#maxRedirects; hop += 1) {
      let response: FetchResponseLike;
      try {
        response = await this.#fetcher(current, { redirect: "manual" });
      } catch (cause) {
        throw new ManifestFetchError(
          methodUrl,
          cause instanceof Error ? cause.message : String(cause),
        );
      }
      if (REDIRECT_STATUSES.has(response.status)) {
        const location = response.headers.get("location");
        if (location === null) {
          throw new ManifestFetchError(
            methodUrl,
            `redirect status ${response.status} without Location header`,
          );
        }
        const target = new URL(location, current);
        if (target.origin !== methodOrigin) {
          throw new CrossOriginRedirectError(methodUrl, target.href);
        }
        current = target.href;
        continue;
      }
      if (!response.ok) {
        throw new ManifestFetchError(methodUrl, `HTTP status ${response.status}`);
      }
      let body: unknown;
      try {
        body = await response.json();
      } catch (cause) {
        throw new ManifestFetchError(
          methodUrl,
          `invalid JSON: ${cause instanceof Error ? cause.message : String(cause)}`,
        );
      }
      return parseManifest(methodUrl, body);
    }
    throw new ManifestFetchError(
      methodUrl,
      `too many redirects (limit ${this.#maxRedirects})`,
    );
  }

  /**
   * 注册 handler：获取清单并校验 handler 来源被
   * default_applications 或 supported_origins 授权。
   */
  async register(methodUrl: string, handlerOrigin: string): Promise<void> {
    const origin = new URL(handlerOrigin).origin;
    const manifest = await this.fetchManifest(methodUrl);
    if (!isHandlerAuthorized(manifest, origin)) {
      throw new HandlerNotAuthorizedError(methodUrl, origin);
    }
    this.#manifests.set(methodUrl, manifest);
    let handlers = this.#handlers.get(methodUrl);
    if (handlers === undefined) {
      handlers = new Set();
      this.#handlers.set(methodUrl, handlers);
    }
    handlers.add(origin);
  }

  /**
   * 重新校验某方式 URL 的清单。清单被撤销（获取失败）时移除全部
   * 已注册 handler；清单变更后不再被授权的 handler 同样被移除。
   * 返回仍有效的 handler 来源列表。
   */
  async refresh(methodUrl: string): Promise<string[]> {
    let manifest: PaymentMethodManifest;
    try {
      manifest = await this.fetchManifest(methodUrl);
    } catch {
      this.#manifests.delete(methodUrl);
      this.#handlers.delete(methodUrl);
      return [];
    }
    this.#manifests.set(methodUrl, manifest);
    const handlers = this.#handlers.get(methodUrl);
    if (handlers === undefined) {
      return [];
    }
    for (const origin of [...handlers]) {
      if (!isHandlerAuthorized(manifest, origin)) {
        handlers.delete(origin);
      }
    }
    return [...handlers];
  }

  /** handler 是否仍响应该方式 URL（已注册且清单仍授权）。 */
  canHandle(methodUrl: string, handlerOrigin: string): boolean {
    const handlers = this.#handlers.get(methodUrl);
    if (handlers === undefined) {
      return false;
    }
    let origin: string;
    try {
      origin = new URL(handlerOrigin).origin;
    } catch {
      return false;
    }
    return handlers.has(origin);
  }

  /** 主动注销。 */
  unregister(methodUrl: string, handlerOrigin: string): boolean {
    const handlers = this.#handlers.get(methodUrl);
    if (handlers === undefined) {
      return false;
    }
    return handlers.delete(new URL(handlerOrigin).origin);
  }
}
