/**
 * Cookie 携带决策引擎。
 *
 * 关键不变量：
 * - 缺少带外 trustedRulesRoot / 证明束 -> 全部拒绝，不回退内置规则。
 * - 跨站闩锁单调：链路任一跨站后，303 回到原站点也不能洗白。
 * - Lax 资格只在初始请求时刻评估一次；303 降级不重新激活。
 * - LAX-allow-unsafe 宽限为 120000ms 硬边界（age < 120000 才有效）。
 * - BFCache 在途请求使用发起时快照，策略版本以快照为准。
 */

import type {
  CarryDecision,
  CookieDecision,
  CookieInput,
  DenyReason,
  HopTrace,
  RequestInput,
  SiteKey,
} from './types.ts';
import { sha256 } from './crypto.ts';
import type { HashFn } from './merkle.ts';
import { ancestorChainSameSite, parseUrl, sameSite, siteKeyOf, topLevelPartitionKey } from './site.ts';
import type { PslResolution } from './psl.ts';
import { RulesVerifier, type RulesProofBundle, type TrustAnchor } from './rules.ts';

export const LAX_GRACE_WINDOW_MS = 120_000;

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS', 'TRACE']);

export interface DecisionContext {
  readonly request: RequestInput;
  readonly cookies: readonly CookieInput[];
  /** 带外信任锚；null 表示宿主未提供外部根。 */
  readonly trustAnchor: TrustAnchor | null;
  /** 规则证明束；null 同样构成 missing-external-root 拒绝。 */
  readonly proofBundle: RulesProofBundle | null;
  /** 决策重算时刻（无缓存参考对照用，不影响语义）。 */
  readonly nowMs: number;
  readonly hash?: HashFn;
}

function isSafeMethod(method: string): boolean {
  return SAFE_METHODS.has(method.toUpperCase());
}

function redirectMethod(current: string, status: number): string {
  if (status === 303) {
    return 'GET';
  }
  if (status === 307 || status === 308) {
    return current;
  }
  // 301/302：历史行为下非 HEAD 降级为 GET。
  return current.toUpperCase() === 'HEAD' ? 'HEAD' : 'GET';
}

function domainMatches(cookieDomain: string, host: string): boolean {
  const domain = cookieDomain.startsWith('.') ? cookieDomain.slice(1) : cookieDomain;
  return host === domain || host.endsWith(`.${domain}`);
}

function pathMatches(cookiePath: string, requestPath: string): boolean {
  if (requestPath === cookiePath) {
    return true;
  }
  if (requestPath.startsWith(cookiePath)) {
    return cookiePath.endsWith('/') || requestPath.charAt(cookiePath.length) === '/';
  }
  return false;
}

function rejectAll(cookies: readonly CookieInput[], reason: DenyReason, base: Omit<CarryDecision, 'cookies'>): CarryDecision {
  return {
    ...base,
    cookies: cookies.map((cookie) => ({ name: cookie.name, send: false, reason })),
  };
}

export function decideCookieCarry(context: DecisionContext): CarryDecision {
  const { request, cookies, trustAnchor, proofBundle, nowMs } = context;
  const hash = context.hash ?? sha256;
  const verifier = new RulesVerifier(trustAnchor, hash);

  const initialParsed = parseUrl(request.url);
  const baseTrace: HopTrace[] = [];

  const base: Omit<CarryDecision, 'cookies' | 'rulesVerification'> = {
    policyVersion: request.policyVersion,
    effectiveMethod: request.method.toUpperCase(),
    finalUrl: initialParsed.url,
    crossedSiteLatch: false,
    sameSiteContext: false,
    laxNavigationEligible: false,
    laxUnsafeGraceEligible: false,
    partitionKey: null,
    trace: baseTrace,
    computedAtMs: nowMs,
  };

  const rootResult = verifier.verifyRoot(proofBundle);
  if (!rootResult.ok) {
    return rejectAll(cookies, rootResult.reason, {
      ...base,
      rulesVerification: { accepted: false, reason: rootResult.reason, matchedRuleType: null },
    });
  }

  // 策略版本必须与受签根绑定，防止暂定导航期间升版影响在途请求。
  if (rootResult.policyVersion !== request.policyVersion) {
    return rejectAll(cookies, 'untrusted-rules-root', {
      ...base,
      rulesVerification: { accepted: false, reason: 'untrusted-rules-root', matchedRuleType: null },
    });
  }

  const bundle = proofBundle!;
  const resolutions = new Map<string, PslResolution>();
  const proofFailure = { value: false as boolean };
  const resolve = (host: string): PslResolution => {
    let resolution = resolutions.get(host);
    if (!resolution) {
      try {
        resolution = verifier.verifyHostProof(bundle, rootResult.merkleRoot, host);
      } catch {
        proofFailure.value = true;
        resolution = { registrableDomain: host, matchedRuleType: 'default', matchedRuleKey: null };
      }
      resolutions.set(host, resolution);
    }
    return resolution;
  };

  let matchedRuleType: PslResolution['matchedRuleType'] = resolve(initialParsed.host).matchedRuleType;
  if (proofFailure.value) {
    return rejectAll(cookies, 'invalid-rules-proof', {
      ...base,
      rulesVerification: { accepted: false, reason: 'invalid-rules-proof', matchedRuleType: null },
    });
  }

  const initialSite: SiteKey = siteKeyOf(initialParsed, resolve(initialParsed.host));

  // 祖先链同站性：不透明身份 / sandbox 去源 / 跨协议均使初始上下文跨站。
  const ancestorsSameSite = ancestorChainSameSite(request.ancestorChain, initialSite, resolve);

  // 重定向链状态机：跨站闩锁单调，方法按状态码演变。
  let latch = !ancestorsSameSite;
  let method = request.method.toUpperCase();
  let currentParsed = initialParsed;
  let methodDowngradedBy303 = false;
  baseTrace.push({
    hop: 0,
    url: initialParsed.url,
    method,
    status: null,
    crossSite: latch,
    latch,
  });

  request.redirects.forEach((hop, index) => {
    method = redirectMethod(method, hop.status);
    if (hop.status === 303 && method !== request.method.toUpperCase()) {
      methodDowngradedBy303 = true;
    }
    const nextParsed = parseUrl(hop.location);
    matchedRuleType = resolve(nextParsed.host).matchedRuleType;
    const nextSite = siteKeyOf(nextParsed, resolve(nextParsed.host));
    const hopCrossSite = !sameSite(nextSite, initialSite);
    if (hopCrossSite) {
      latch = true;
    }
    currentParsed = nextParsed;
    baseTrace.push({
      hop: index + 1,
      url: nextParsed.url,
      method,
      status: hop.status,
      crossSite: hopCrossSite,
      latch,
    });
  });

  if (proofFailure.value) {
    return rejectAll(cookies, 'invalid-rules-proof', {
      ...base,
      effectiveMethod: method,
      finalUrl: currentParsed.url,
      crossedSiteLatch: latch,
      trace: baseTrace,
      rulesVerification: { accepted: false, reason: 'invalid-rules-proof', matchedRuleType: null },
    });
  }

  // Lax 资格仅由初始请求决定，重定向（含 303）不得重新激活。
  const initialSafe = isSafeMethod(request.method);
  const laxNavigationEligible = request.topLevelNavigation && initialSafe;

  const partitionKey = topLevelPartitionKey(request.ancestorChain, resolve);

  const decisionBase: CarryDecision = {
    ...base,
    effectiveMethod: method,
    finalUrl: currentParsed.url,
    crossedSiteLatch: latch,
    sameSiteContext: !latch,
    laxNavigationEligible,
    // 引擎级标记：记录“可能进入宽限评估”的导航形态，逐 Cookie 年龄在下方硬边界判定。
    laxUnsafeGraceEligible: request.topLevelNavigation && !initialSafe,
    partitionKey,
    rulesVerification: { accepted: true, reason: null, matchedRuleType },
  };

  const cookieDecisions: CookieDecision[] = cookies.map((cookie) =>
    decideOne(cookie, {
      request,
      finalHost: currentParsed.host,
      finalPath: currentParsed.path,
      finalScheme: currentParsed.scheme,
      latch,
      laxNavigationEligible,
      laxUnsafeGraceEligible: request.topLevelNavigation && !initialSafe,
      partitionKey,
      methodDowngradedBy303,
    }),
  );

  return { ...decisionBase, cookies: cookieDecisions };
}

interface OneContext {
  readonly request: RequestInput;
  readonly finalHost: string;
  readonly finalPath: string;
  readonly finalScheme: string;
  readonly latch: boolean;
  readonly laxNavigationEligible: boolean;
  readonly laxUnsafeGraceEligible: boolean;
  readonly partitionKey: SiteKey | null;
  readonly methodDowngradedBy303: boolean;
}

function decideOne(cookie: CookieInput, context: OneContext): CookieDecision {
  const deny = (reason: DenyReason): CookieDecision => ({ name: cookie.name, send: false, reason });

  if (!domainMatches(cookie.domain, context.finalHost)) {
    return deny('domain-mismatch');
  }
  if (!pathMatches(cookie.path, context.finalPath)) {
    return deny('path-mismatch');
  }
  if (cookie.sameSite === 'none' && !cookie.secure) {
    return deny('insecure-none-cookie');
  }
  if (cookie.secure && context.finalScheme !== 'https') {
    return deny('secure-context-required');
  }

  // CHIPS 分区 Cookie：必须存在不透明之外的分区键且严格绑定。
  if (cookie.partitioned) {
    if (context.partitionKey === null) {
      return deny('opaque-partition');
    }
    if (
      !cookie.partitionKey ||
      !sameSite(cookie.partitionKey, context.partitionKey)
    ) {
      return deny('partition-key-mismatch');
    }
  }

  // 同站上下文：Strict/Lax/None 均可发送（分区 Cookie 按分区语义仍要求键匹配，已在上面处理）。
  if (!context.latch) {
    return { name: cookie.name, send: true, reason: null };
  }

  // 以下为跨站上下文（含被 303 洗白尝试的链路，闩锁已置位）。
  if (cookie.sameSite === 'strict') {
    return deny('same-site-strict-blocked');
  }

  if (cookie.sameSite === 'lax') {
    // 303 之后即使方法变为 GET 也不允许重新激活 Lax 资格。
    if (context.methodDowngradedBy303) {
      return deny('303-reactivation-denied');
    }
    if (isSafeMethod(context.request.method)) {
      if (!context.laxNavigationEligible) {
        return deny('lax-cross-site-blocked');
      }
      return { name: cookie.name, send: true, reason: null };
    }
    if (!context.laxUnsafeGraceEligible) {
      return deny('lax-unsafe-method-blocked');
    }
    // 不安全方法的顶层导航：仅新鲜 Cookie 在 120s 宽限内放行。
    const age = context.request.initiatedAtMs - cookie.createdAtMs;
    if (age >= 0 && age < LAX_GRACE_WINDOW_MS) {
      return { name: cookie.name, send: true, reason: null };
    }
    return deny(age >= LAX_GRACE_WINDOW_MS ? 'lax-grace-expired' : 'lax-unsafe-method-blocked');
  }

  // SameSite=None：非分区 Cookie 在跨站上下文拒绝；分区 Cookie 已完成键绑定。
  if (!cookie.partitioned) {
    return deny('cross-site-redirect-latch');
  }
  return { name: cookie.name, send: true, reason: null };
}
