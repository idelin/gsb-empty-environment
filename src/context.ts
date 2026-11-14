/**
 * 调用方上下文快照。
 *
 * 安全关键属性必须在调用发生时从浏览器实时读取（不可由调用方伪造）。
 * 在真实页面中使用 readBrowserContext()；测试中可直接构造字面量。
 */
export interface CallerContext {
  /** window.isSecureContext —— 必须处于受保护上下文（HTTPS/localhost）。 */
  readonly secureContext: boolean;
  /** navigator.userActivation.isActive —— 调用瞬间存在瞬时用户激活（手势）。 */
  readonly userActivation: boolean;
  /** window.top === window.self —— 非跨源 iframe 嵌套。 */
  readonly sameOriginFrame: boolean;
  /** 脚本是否来自第一方源（非注入的第三方脚本）。 */
  readonly firstPartyScript: boolean;
  /** Permissions-Policy 是否允许 credential-isolation 特性。 */
  readonly policyAllows: boolean;
  /** 用户手势标识（如同一次 click 的事件 id），用于"每次手势限次读取"。 */
  readonly gestureId?: string;
}

/** 浏览器环境实时采集上下文。 */
export function readBrowserContext(
  win: {
    isSecureContext?: boolean;
    top?: unknown;
    self?: unknown;
    navigator?: { userActivation?: { isActive?: boolean } };
    document?: { permissionsPolicy?: { allowsFeature(f: string): boolean } };
  } = globalThis as never,
): CallerContext {
  const policy = win.document?.permissionsPolicy;
  return {
    secureContext: win.isSecureContext === true,
    userActivation: win.navigator?.userActivation?.isActive === true,
    sameOriginFrame: win.top !== undefined && win.top === win.self,
    firstPartyScript: true, // 第一方打包代码内调用；第三方脚本应显式置 false
    policyAllows: policy ? policy.allowsFeature('credential-isolation') : true,
  };
}
