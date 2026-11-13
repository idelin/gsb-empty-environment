import type { RequestPolicy, RequestInfo, ResourceType } from "./policy.ts";

/**
 * 适配层只依赖 Playwright/Puppeteer 的结构形状，
 * 不 import 框架本身，渲染服务无需为判定包安装浏览器依赖。
 */

type PlaywrightRequestLike = {
  url(): string;
  resourceType(): string;
  frame(): { url(): string } | null | undefined;
  isNavigationRequest?(): boolean;
  redirectChain?(): ReadonlyArray<{ url(): string }>;
};

type PuppeteerRequestLike = {
  url(): string;
  resourceType(): string;
  frame(): { url(): string } | null | undefined;
  isNavigationRequest?(): boolean;
  redirectChain?(): ReadonlyArray<{ url(): string }>;
};

const RESOURCE_TYPE_MAP: Record<string, ResourceType> = {
  document: "document",
  stylesheet: "stylesheet",
  image: "image",
  media: "media",
  font: "font",
  script: "script",
  xhr: "xhr",
  fetch: "fetch",
  websocket: "websocket",
  eventsource: "eventsource",
  // Puppeteer 独有名称
  img: "image",
  font_face: "font",
  other: "other",
};

function normalizeResourceType(raw: string): ResourceType {
  return RESOURCE_TYPE_MAP[raw.toLowerCase()] ?? "other";
}

function toRequestInfo(
  request: PlaywrightRequestLike | PuppeteerRequestLike,
): RequestInfo {
  const frame = request.frame();
  const isNavigation = request.isNavigationRequest?.() ?? false;
  return {
    url: request.url(),
    type: normalizeResourceType(request.resourceType()),
    // 导航请求没有“发起页面”，按主页面处理；iframe 文档仍带父 frame
    initiatorFrameUrl: isNavigation && frame === null ? null : (frame?.url() ?? null),
    redirectChain: (request.redirectChain?.() ?? []).map((entry) => ({
      url: entry.url(),
    })),
  };
}

/** Playwright page.on("request", handler) / route 拦截回调。 */
export function playwrightRequestHandler(policy: RequestPolicy) {
  return async (request: PlaywrightRequestLike): Promise<boolean> => {
    const decision = await policy.decide(toRequestInfo(request));
    return decision.allowed;
  };
}

/**
 * Playwright route 拦截：返回 true 放行，否则 abort。
 * 用法：page.route("**", async (route) => {
 *   if (await playwrightRouteGuard(policy)(route.request)) await route.continue();
 *   else await route.abort();
 * });
 */
export function playwrightRouteGuard(policy: RequestPolicy) {
  return async (request: PlaywrightRequestLike): Promise<boolean> => {
    const decision = await policy.decide(toRequestInfo(request));
    return decision.allowed;
  };
}

/** Puppeteer page.on("request", handler) / request interception 回调。 */
export function puppeteerRequestHandler(policy: RequestPolicy) {
  return async (request: PuppeteerRequestLike): Promise<boolean> => {
    const decision = await policy.decide(toRequestInfo(request));
    return decision.allowed;
  };
}
