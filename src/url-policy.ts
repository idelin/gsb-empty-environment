import type { RegisteredKeyEntry } from './types.ts';

export type UrlViolation =
  | 'not_https'
  | 'has_userinfo'
  | 'has_query'
  | 'has_fragment'
  | 'has_dotdot_segment'
  | 'host_mismatch'
  | 'non_default_port'
  | 'path_prefix_mismatch'
  | 'malformed_url';

export interface UrlCheckResult {
  ok: boolean;
  /** 解析后的规范化 URL；解析失败时为 null。 */
  url: URL | null;
  violation?: UrlViolation;
  detail?: string;
}

/**
 * 在 URL 规范化之前检测原始路径中的 `..` 段。
 * WHATWG URL 会把 `/a/../b` 解析成 `/b`，所以必须基于原始字符串判断，
 * 同时识别百分号编码（%2e%2e 及其大小写混用）形式。
 */
function hasDotDotSegment(rawUrl: string, parsed: URL): boolean {
  const schemeIndex = rawUrl.indexOf('://');
  let authorityAndRest = schemeIndex >= 0 ? rawUrl.slice(schemeIndex + 3) : rawUrl;
  const pathSlash = authorityAndRest.indexOf('/');
  let rawPath: string;
  if (pathSlash >= 0) {
    rawPath = authorityAndRest.slice(pathSlash + 1);
  } else {
    rawPath = '';
  }
  const queryOrHash = rawPath.search(/[?#]/);
  if (queryOrHash >= 0) {
    rawPath = rawPath.slice(0, queryOrHash);
  }
  const segments = rawPath.split('/');
  for (const segment of segments) {
    if (segment === '..') {
      return true;
    }
    let decoded = segment;
    try {
      decoded = decodeURIComponent(segment);
    } catch {
      // 非法百分号编码无法安全规范化，按危险段处理。
      return true;
    }
    if (decoded === '..') {
      return true;
    }
  }
  // 防御性兜底：规范化后的路径若仍残留 .. 段也拒绝。
  for (const segment of parsed.pathname.split('/')) {
    if (segment === '..') {
      return true;
    }
  }
  return false;
}

/**
 * 判定 jku/x5u URL 是否精确匹配该 kid 登记的来源：
 *
 * - 必须是 https；
 * - 不得携带 userinfo、查询串、片段；
 * - 路径不得包含 `..` 段（含百分号编码形式）；
 * - 主机不分大小写地等于登记主机，且不允许非 443 显式端口；
 * - 路径逐字等于登记前缀，或在前缀的路径段边界之后继续延伸。
 */
export function checkKeyUrl(rawUrl: unknown, entry: RegisteredKeyEntry): UrlCheckResult {
  if (typeof rawUrl !== 'string' || rawUrl.length === 0) {
    return { ok: false, url: null, violation: 'malformed_url', detail: 'header value is not a non-empty string' };
  }

  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    return { ok: false, url: null, violation: 'malformed_url', detail: `unparseable URL: ${rawUrl}` };
  }

  if (url.protocol !== 'https:') {
    return { ok: false, url, violation: 'not_https', detail: `protocol is ${url.protocol}` };
  }
  if (url.username !== '' || url.password !== '') {
    return { ok: false, url, violation: 'has_userinfo', detail: 'userinfo (user:password@) is not allowed' };
  }
  if (url.search !== '') {
    return { ok: false, url, violation: 'has_query', detail: 'query string is not allowed' };
  }
  if (url.hash !== '') {
    return { ok: false, url, violation: 'has_fragment', detail: 'URL fragment is not allowed' };
  }
  if (hasDotDotSegment(rawUrl, url)) {
    return { ok: false, url, violation: 'has_dotdot_segment', detail: 'path contains a ".." segment' };
  }
  if (url.hostname.toLowerCase() !== entry.allowedHost.toLowerCase()) {
    return { ok: false, url, violation: 'host_mismatch', detail: `host ${url.hostname} is not ${entry.allowedHost}` };
  }
  if (url.port !== '' && url.port !== '443') {
    return { ok: false, url, violation: 'non_default_port', detail: `port ${url.port} is not allowed` };
  }

  const prefix = entry.allowedPathPrefix;
  const path = url.pathname;
  const prefixOk =
    path === prefix ||
    (prefix.endsWith('/') ? path.startsWith(prefix) : path.startsWith(`${prefix}/`));
  if (!prefixOk) {
    return { ok: false, url, violation: 'path_prefix_mismatch', detail: `path ${path} is outside registered prefix ${prefix}` };
  }

  return { ok: true, url };
}
