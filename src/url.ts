import { fail } from './errors.ts';

export const SECURE_SCHEMES = new Set(['http:', 'https:']);

const VALID_PERCENT = /%[0-9a-fA-F]{2}/g;

export interface NormalizedUrl {
  readonly scheme: string;
  readonly origin: string;
  readonly pathname: string;
  readonly segments: readonly string[];
}

export interface ScopeMatch {
  readonly scopePath: string;
  readonly scopeSegments: readonly string[];
}

function rejectEncodedSeparators(rawPath: string): void {
  if (rawPath.includes('\\')) {
    fail(
      'ENCODED_SEPARATOR_AMBIGUITY',
      '路径中禁止反斜杠，避免被不同解析器当作分隔符。',
      { path: rawPath },
    );
  }

  const withoutValidEscapes = rawPath.replace(VALID_PERCENT, '');
  if (withoutValidEscapes.includes('%')) {
    fail(
      'INVALID_PERCENT_ENCODING',
      '路径包含非法的百分号转义序列。',
      { path: rawPath },
    );
  }

  for (const match of rawPath.matchAll(VALID_PERCENT)) {
    const decoded = decodeURIComponent(match[0]);
    if (decoded === '/' || decoded === '\\') {
      fail(
        'ENCODED_SEPARATOR_AMBIGUITY',
        '编码后的路径分隔符会引入段边界歧义，必须拒绝。',
        { path: rawPath, escape: match[0] },
      );
    }
  }
}

function toSegments(pathname: string): string[] {
  return pathname.split('/').filter((segment) => segment.length > 0);
}

export function normalizeUrl(input: string, base?: string): NormalizedUrl {
  if (input.includes('\\')) {
    fail(
      'ENCODED_SEPARATOR_AMBIGUITY',
      '路径中禁止反斜杠，避免被不同解析器当作分隔符。',
      { input },
    );
  }

  let parsed: URL;
  try {
    parsed = base === undefined ? new URL(input) : new URL(input, base);
  } catch {
    fail('INVALID_URL', '无法解析为合法 URL。', { input: String(input) });
  }

  if (!SECURE_SCHEMES.has(parsed.protocol)) {
    fail('UNSUPPORTED_SCHEME', '仅支持 http/https 来源的 URL。', {
      input: parsed.href,
      scheme: parsed.protocol,
    });
  }

  rejectEncodedSeparators(parsed.pathname);

  const segments = toSegments(parsed.pathname);
  return {
    scheme: parsed.protocol,
    origin: parsed.origin,
    pathname: parsed.pathname,
    segments,
  };
}

export function scriptDirectory(url: NormalizedUrl): ScopeMatch {
  const segments = url.segments.slice(0, -1);
  return {
    scopePath: `/${segments.join('/')}/`,
    scopeSegments: segments,
  };
}

export function scopeFromUrl(url: NormalizedUrl): ScopeMatch {
  const segments = [...url.segments];
  return {
    scopePath: `/${segments.join('/')}/`,
    scopeSegments: segments,
  };
}

export function sameOrigin(left: NormalizedUrl, right: NormalizedUrl): boolean {
  return left.origin === right.origin;
}

export function isPathPrefix(
  prefix: readonly string[],
  candidate: readonly string[],
): boolean {
  if (prefix.length > candidate.length) {
    return false;
  }
  return prefix.every((segment, index) => candidate[index] === segment);
}

export function scopeContains(scope: ScopeMatch, url: NormalizedUrl): boolean {
  return isPathPrefix(scope.scopeSegments, url.segments);
}

export type ScopeRelation = 'equal' | 'contains' | 'contained-by' | 'disjoint';

export function compareScopes(left: ScopeMatch, right: ScopeMatch): ScopeRelation {
  const leftPrefix = isPathPrefix(left.scopeSegments, right.scopeSegments);
  const rightPrefix = isPathPrefix(right.scopeSegments, left.scopeSegments);

  if (leftPrefix && rightPrefix) {
    return 'equal';
  }
  if (leftPrefix) {
    return 'contains';
  }
  if (rightPrefix) {
    return 'contained-by';
  }
  return 'disjoint';
}
