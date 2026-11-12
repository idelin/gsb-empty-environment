import {
  OPTIONALLY_BLOCKABLE,
  type DocumentPolicy,
  type RequestPlan,
  type ResourceType,
} from "./types.ts";
import { DEFAULT_PORT_MAP, mapPort } from "./ports.ts";

const UPGRADE_SCHEME: Record<string, string> = {
  "http:": "https:",
  "ws:": "wss:",
};

export interface PlanInput {
  url: string;
  resourceType: ResourceType;
  policy: DocumentPolicy;
  /** 端口映射规则，默认仅 80→443。 */
  portMap?: Readonly<Record<number, number>>;
  /** 相对 URL 的解析基准（重定向时使用）。 */
  baseUrl?: string;
}

function explicitPort(url: URL): number | null {
  if (url.port === "") return null;
  return Number(url.port);
}

function upgradeUrl(
  raw: string,
  baseUrl: string | undefined,
  portMap: Readonly<Record<number, number>>,
): string {
  const url = new URL(raw, baseUrl);
  url.protocol = UPGRADE_SCHEME[url.protocol] ?? url.protocol;
  const mapped = mapPort(explicitPort(url), portMap);
  if (mapped === null) {
    url.port = "";
  } else {
    url.port = String(mapped);
  }
  return url.toString();
}

/**
 * 资源类型与文档策略共同决定升级或阻断：
 * - 非安全文档：无混合内容问题，直接放行。
 * - upgradeInsecureRequests：一切 http/ws 子资源升级。
 * - blockAllMixedContent：未升级的一律阻断。
 * - 默认：可选项阻断类型（image/audio/video）自动升级，其余阻断。
 * 内网地址不做任何豁免，规则一致适用。
 */
export function planRequest(input: PlanInput): RequestPlan {
  const { resourceType, policy } = input;
  const portMap = input.portMap ?? DEFAULT_PORT_MAP;
  const originalUrl = new URL(input.url, input.baseUrl).toString();
  const scheme = new URL(originalUrl).protocol;

  const upgradableScheme = scheme in UPGRADE_SCHEME;
  if (!upgradableScheme) {
    return { action: "allow", url: originalUrl, originalUrl, reason: "non-http-scheme" };
  }
  if (!policy.secureContext) {
    return { action: "allow", url: originalUrl, originalUrl, reason: "insecure-document" };
  }

  const upgraded = upgradeUrl(originalUrl, undefined, portMap);

  if (policy.upgradeInsecureRequests) {
    return { action: "upgrade", url: upgraded, originalUrl, reason: "policy-upgrade-insecure-requests" };
  }
  if (policy.blockAllMixedContent) {
    return { action: "block", url: originalUrl, originalUrl, reason: "policy-block-all-mixed-content" };
  }
  if (OPTIONALLY_BLOCKABLE.has(resourceType)) {
    return { action: "upgrade", url: upgraded, originalUrl, reason: "auto-upgrade-optionally-blockable" };
  }
  return { action: "block", url: originalUrl, originalUrl, reason: "blockable-mixed-content" };
}
