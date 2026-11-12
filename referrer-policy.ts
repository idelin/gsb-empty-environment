/**
 * 引用策略（Referrer-Policy）组件
 *
 * 实现 Fetch / Referrer-Policy 规范中的引用地址计算与重定向逐跳收紧逻辑：
 * - 计算前先移除来源 URL 的凭据（userinfo）与片段（fragment）；
 * - 路径与查询是否保留完全由显式策略决定；
 * - 重定向链上的策略只能收紧（更严格），响应中的更宽松策略不会放宽当前策略。
 *
 * 仅依赖 WHATWG URL，可在浏览器与 Node 18+ 中直接导入使用。
 */

/** 规范定义的八种引用策略名。 */
export const REFERRER_POLICIES = [
  'no-referrer',
  'no-referrer-when-downgrade',
  'strict-origin-when-cross-origin',
  'same-origin',
  'origin-when-cross-origin',
  'strict-origin',
  'origin',
  'unsafe-url',
] as const;

export type ReferrerPolicyName = (typeof REFERRER_POLICIES)[number];

/** 显式入参允许空串，空串按规范回退到默认策略。 */
export type ReferrerPolicyInput = ReferrerPolicyName | '';

/**
 * 空串策略的默认回退值，也是浏览器的默认行为。
 */
export const DEFAULT_REFERRER_POLICY: ReferrerPolicyName =
  'strict-origin-when-cross-origin';

/**
 * 策略严格度顺序，从最宽松到最严格（依据 Fetch 规范的
 * "determine request's referrer policy" 级联顺序）。
 * 重定向组合策略时，只接受严格度更高的候选策略。
 */
const POLICY_STRENGTH_ORDER: readonly ReferrerPolicyName[] = [
  'unsafe-url',
  'origin',
  'strict-origin',
  'origin-when-cross-origin',
  'same-origin',
  'strict-origin-when-cross-origin',
  'no-referrer-when-downgrade',
  'no-referrer',
];

const POLICY_STRENGTH: Readonly<Record<ReferrerPolicyName, number>> =
  Object.freeze(
    Object.fromEntries(
      POLICY_STRENGTH_ORDER.map((policy, index) => [policy, index]),
    ) as Record<ReferrerPolicyName, number>,
  );

export function isReferrerPolicyName(
  value: string,
): value is ReferrerPolicyName {
  return (REFERRER_POLICIES as readonly string[]).includes(value);
}

/**
 * 归一化显式策略：空串回退到默认策略，无法识别的令牌抛出 TypeError。
 */
export function normalizeReferrerPolicy(
  policy: ReferrerPolicyInput | string,
): ReferrerPolicyName {
  if (policy === '') {
    return DEFAULT_REFERRER_POLICY;
  }
  if (!isReferrerPolicyName(policy)) {
    throw new TypeError(
      `Invalid referrer policy "${policy}". Expected one of: ${REFERRER_POLICIES.join(
        ', ',
      )}, or an empty string.`,
    );
  }
  return policy;
}

function toURL(value: string | URL): URL {
  return value instanceof URL ? new URL(value.href) : new URL(value);
}

/**
 * 移除引用地址中的凭据（username/password）与片段（hash），
 * 返回新的 URL 对象，不修改入参。路径与查询保持原样。
 */
export function sanitizeReferrerURL(value: string | URL): URL {
  const url = toURL(value);
  url.username = '';
  url.password = '';
  url.hash = '';
  return url;
}

/**
 * 序列化来源（origin），始终以 "/" 结尾，例如 https://a.example:8443/。
 * WHATWG URL 的 host 已省略默认端口。
 */
export function serializeOrigin(value: string | URL): string {
  const url = toURL(value);
  return `${url.protocol}//${url.host}/`;
}

/** 同源判定：协议、主机（含端口）完全一致。 */
export function isSameOrigin(
  left: string | URL,
  right: string | URL,
): boolean {
  const leftURL = toURL(left);
  const rightURL = toURL(right);
  return leftURL.protocol === rightURL.protocol && leftURL.host === rightURL.host;
}

/**
 * 安全到不安全的降级判定：来源是可信任安全协议（https/wss 等），
 * 而目标不是。降级与是否跨源相互独立。
 */
export function isDowngrade(
  source: string | URL,
  target: string | URL,
): boolean {
  const trustworthyProtocols = new Set(['https:', 'wss:', 'file:']);
  const sourceURL = toURL(source);
  const targetURL = toURL(target);
  return (
    trustworthyProtocols.has(sourceURL.protocol) &&
    !trustworthyProtocols.has(targetURL.protocol)
  );
}

/**
 * 按显式策略计算发往目标 URL 的 Referer 头取值。
 *
 * @param sourceURL 来源页面地址，凭据与片段会在计算前移除
 * @param targetURL 目标请求地址
 * @param policy 显式引用策略，空串回退到 strict-origin-when-cross-origin
 * @returns Referer 头取值；空字符串表示不发送 Referer
 */
export function computeReferrer(
  sourceURL: string | URL,
  targetURL: string | URL,
  policy: ReferrerPolicyInput | string,
): string {
  const current = sanitizeReferrerURL(sourceURL);
  const target = toURL(targetURL);
  const normalizedPolicy = normalizeReferrerPolicy(policy);
  const sameOrigin = isSameOrigin(current, target);
  const downgrade = isDowngrade(current, target);

  switch (normalizedPolicy) {
    case 'no-referrer':
      return '';
    case 'unsafe-url':
      return current.href;
    case 'origin':
      return serializeOrigin(current);
    case 'strict-origin':
      return downgrade ? '' : serializeOrigin(current);
    case 'same-origin':
      return sameOrigin ? current.href : '';
    case 'origin-when-cross-origin':
      return sameOrigin ? current.href : serializeOrigin(current);
    case 'no-referrer-when-downgrade':
      return downgrade ? '' : current.href;
    case 'strict-origin-when-cross-origin':
      if (downgrade) {
        return '';
      }
      return sameOrigin ? current.href : serializeOrigin(current);
  }
}

/**
 * 组合重定向响应中的 Referrer-Policy 令牌（Fetch 规范
 * "combine referrer policies"）。
 *
 * - 响应未携带任何可识别策略时，维持当前策略；
 * - 无法识别的令牌被忽略；
 * - 仅当候选策略严格度更高时才替换当前策略，逐跳只紧不松。
 *
 * @param currentPolicy 当前请求已生效的策略（空串先归一化为默认策略）
 * @param responsePolicies 重定向响应 Referrer-Policy 头中的全部令牌
 */
export function combineReferrerPolicy(
  currentPolicy: ReferrerPolicyInput | string,
  responsePolicies: Iterable<string>,
): ReferrerPolicyName {
  let policy = normalizeReferrerPolicy(currentPolicy);
  for (const token of responsePolicies) {
    if (!isReferrerPolicyName(token)) {
      continue;
    }
    if (POLICY_STRENGTH[token] > POLICY_STRENGTH[policy]) {
      policy = token;
    }
  }
  return policy;
}

export interface RedirectHop {
  /** 本次重定向的 Location 目标地址。 */
  location: string | URL;
  /** 重定向响应 Referrer-Policy 头携带的策略令牌，可含无效令牌与空串。 */
  referrerPolicy?: readonly string[];
}

export interface RedirectHopResult {
  /** 跳序号，0 表示最初请求，之后依次为各次重定向。 */
  hop: number;
  /** 本跳实际请求的 URL。 */
  requestURL: string;
  /** 本跳生效的引用策略。 */
  policy: ReferrerPolicyName;
  /** 本跳发送的 Referer 头取值，空字符串表示不发送。 */
  referrer: string;
  /** 规范维护的内部引用地址（已移除凭据与片段）。 */
  internalReferrer: string;
}

/**
 * 沿重定向链逐跳计算引用策略与 Referer 头。
 *
 * 每跳依次执行：
 * 1. 用响应的 Referrer-Policy 令牌组合当前策略（只能收紧）；
 * 2. 仅当上一跳请求地址与 Location 同源时，才把内部引用地址更新为
 *    Location（同样先移除凭据与片段）；跨源重定向保留原引用地址；
 * 3. 按本跳策略与目标地址计算 Referer 头。
 *
 * @param sourceURL 最初来源页面地址
 * @param initialTargetURL 首个请求目标地址
 * @param initialPolicy 首个请求的显式策略
 * @param redirects 按顺序给出的重定向跳描述
 */
export function resolveRedirectChain(
  sourceURL: string | URL,
  initialTargetURL: string | URL,
  initialPolicy: ReferrerPolicyInput | string,
  redirects: readonly RedirectHop[],
): RedirectHopResult[] {
  let policy = normalizeReferrerPolicy(initialPolicy);
  let internalReferrer = sanitizeReferrerURL(sourceURL);
  let currentTarget = toURL(initialTargetURL);

  const results: RedirectHopResult[] = [
    {
      hop: 0,
      requestURL: currentTarget.href,
      policy,
      referrer: computeReferrer(internalReferrer, currentTarget, policy),
      internalReferrer: internalReferrer.href,
    },
  ];

  for (const [index, redirect] of redirects.entries()) {
    policy = combineReferrerPolicy(
      policy,
      redirect.referrerPolicy ?? [],
    );
    const location = toURL(redirect.location);

    if (isSameOrigin(currentTarget, location)) {
      internalReferrer = sanitizeReferrerURL(location);
    }
    currentTarget = location;

    results.push({
      hop: index + 1,
      requestURL: currentTarget.href,
      policy,
      referrer: computeReferrer(internalReferrer, currentTarget, policy),
      internalReferrer: internalReferrer.href,
    });
  }

  return results;
}
