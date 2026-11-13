import type { ValidationIssue } from './types.ts';

export interface LicenseCheck {
  ok: boolean;
  issue?: ValidationIssue;
}

interface AllowEntry {
  host: string;
  wildcard: boolean;
  pathPrefix: string;
}

function compileAllowEntry(raw: string): AllowEntry | null {
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    return null;
  }
  if (parsed.protocol !== 'https:') return null;
  const host = parsed.hostname.toLowerCase();
  return {
    host: host.startsWith('*.') ? host.slice(2) : host,
    wildcard: host.startsWith('*.'),
    pathPrefix: parsed.pathname === '/' ? '/' : parsed.pathname.replace(/\/$/, ''),
  };
}

function hostMatches(entry: AllowEntry, host: string): boolean {
  if (entry.wildcard) {
    return host.endsWith(`.${entry.host}`) && host.length > entry.host.length + 1;
  }
  return host === entry.host;
}

function pathMatches(entry: AllowEntry, pathname: string): boolean {
  if (entry.pathPrefix === '/') return true;
  return pathname === entry.pathPrefix || pathname.startsWith(`${entry.pathPrefix}/`);
}

/**
 * 校验 licenseUrl：
 * 1. 必须是可解析的绝对 URL；
 * 2. 协议必须为 https，且不得携带 userinfo；
 * 3. 默认仅允许 443 端口（显式开启 `allowNonDefaultPort` 后放宽）；
 * 4. host 与路径必须命中白名单（支持 `*.example.com` 子域通配）。
 */
export function checkLicenseUrl(
  rawUrl: string | undefined,
  allowed: string[],
  allowNonDefaultPort = false,
  locator?: string,
): LicenseCheck {
  if (!rawUrl) {
    return {
      ok: false,
      issue: {
        code: 'LICENSE_URL_MISSING',
        message: 'ContentProtection 未声明 licenseUrl',
        locator,
      },
    };
  }

  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    return {
      ok: false,
      issue: {
        code: 'LICENSE_URL_MALFORMED',
        message: `licenseUrl 无法解析: ${rawUrl}`,
        locator,
      },
    };
  }

  if (url.protocol !== 'https:') {
    return {
      ok: false,
      issue: {
        code: 'LICENSE_URL_NOT_HTTPS',
        message: `licenseUrl 必须使用 https，实际为 ${url.protocol}//`,
        locator,
      },
    };
  }
  if (url.username || url.password) {
    return {
      ok: false,
      issue: {
        code: 'LICENSE_URL_MALFORMED',
        message: 'licenseUrl 不得内嵌用户名/密码等凭据',
        locator,
      },
    };
  }
  if (!allowNonDefaultPort && url.port !== '') {
    return {
      ok: false,
      issue: {
        code: 'LICENSE_URL_NOT_ALLOWLISTED',
        message: `licenseUrl 不允许非默认端口 ${url.port}`,
        locator,
      },
    };
  }

  const host = url.hostname.toLowerCase();
  const entries = allowed
    .map(compileAllowEntry)
    .filter((entry): entry is AllowEntry => entry !== null);

  const matched = entries.some(
    (entry) => hostMatches(entry, host) && pathMatches(entry, url.pathname),
  );
  if (!matched) {
    return {
      ok: false,
      issue: {
        code: 'LICENSE_URL_NOT_ALLOWLISTED',
        message: `licenseUrl 不在白名单内: ${rawUrl}`,
        locator,
      },
    };
  }
  return { ok: true };
}
