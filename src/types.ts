export type ResourceType =
  | "script"
  | "style"
  | "image"
  | "audio"
  | "video"
  | "font"
  | "xhr"
  | "fetch"
  | "websocket"
  | "iframe"
  | "worker"
  | "document"
  | "other";

export const OPTIONALLY_BLOCKABLE: ReadonlySet<ResourceType> = new Set([
  "image",
  "audio",
  "video",
]);

export interface DocumentPolicy {
  /** 文档本身是否处于安全上下文（https 页面）。 */
  secureContext: boolean;
  /** CSP upgrade-insecure-requests：所有明文子资源一律升级。 */
  upgradeInsecureRequests: boolean;
  /** CSP block-all-mixed-content：不可升级的一律阻断。 */
  blockAllMixedContent: boolean;
}

export type PlanAction = "allow" | "upgrade" | "block";

export interface RequestPlan {
  action: PlanAction;
  /** 实际应请求的 URL（upgrade 时为升级后的安全 URL）。 */
  url: string;
  /** 规划前的原始 URL。 */
  originalUrl: string;
  reason: string;
}
