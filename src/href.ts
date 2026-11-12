import { HrefValidationError } from './errors.ts';

const DEFAULT_MAX_HREF_LENGTH = 8192;
const CONTROL_CHARACTER = /[\u0000-\u001f\u007f]/;
const PATH_SEPARATOR = /[\/\\]/;

export interface HrefNormalizationOptions {
  /**
   * Authority of the request URL, for example `dav.example.com` or
   * `dav.example.com:8443`. Absolute DAV hrefs are accepted only when their
   * authority matches this value. Path-only hrefs do not use this option.
   */
  requestAuthority?: string;
  /** Explicit opt-in for accepting absolute hrefs without an authority check. */
  allowAnyAuthority?: boolean;
  maxLength?: number;
}

export interface NormalizedHref {
  /** Trimmed input, useful when echoing the client-supplied reference. */
  raw: string;
  /** Canonical absolute path with percent-encoded segments. */
  path: string;
  /** Decoded path segments. Dot segments and encoded separators never appear here. */
  segments: readonly string[];
  /** Present only when the input was an absolute http(s) URI. */
  authority?: string;
}

interface ExtractedReference {
  raw: string;
  path: string;
  authority?: string;
}

interface ParsedPath {
  path: string;
  segments: string[];
}

export function normalizeHref(
  rawHref: string,
  options: HrefNormalizationOptions = {},
): NormalizedHref {
  const extracted = extractReference(rawHref, options);
  const parsed = parsePathSegments(extracted.path, 'resource');
  return {
    raw: extracted.raw,
    path: parsed.path,
    segments: parsed.segments,
    authority: extracted.authority,
  };
}

/**
 * Collection paths may be supplied with a trailing slash and may be the root
 * collection (`/`). The returned canonical path has no trailing slash.
 */
export function normalizeCollectionPath(
  rawPath: string,
  options: HrefNormalizationOptions = {},
): NormalizedHref {
  const extracted = extractReference(rawPath, options);
  const parsed = parsePathSegments(extracted.path, 'collection');
  return {
    raw: extracted.raw,
    path: parsed.path,
    segments: parsed.segments,
    authority: extracted.authority,
  };
}

/**
 * A target must be exactly one path segment below the collection. This
 * deliberately does not use string prefix matching, so
 * `/addressbooks/a/default-other/x.vcf` cannot satisfy a request for
 * `/addressbooks/a/default`.
 */
export function isDirectMember(
  collection: NormalizedHref | string,
  target: NormalizedHref | string,
): boolean {
  const collectionPath =
    typeof collection === 'string'
      ? normalizeCollectionPath(collection)
      : collection;
  const targetPath =
    typeof target === 'string' ? normalizeHref(target) : target;

  if (targetPath.segments.length !== collectionPath.segments.length + 1) {
    return false;
  }

  for (let index = 0; index < collectionPath.segments.length; index += 1) {
    if (collectionPath.segments[index] !== targetPath.segments[index]) {
      return false;
    }
  }

  return true;
}

export function isDirectMemberPath(
  collectionPath: string,
  memberHref: string,
): boolean {
  return isDirectMember(
    normalizeCollectionPath(collectionPath),
    normalizeHref(memberHref),
  );
}

function extractReference(
  input: string,
  options: HrefNormalizationOptions,
): ExtractedReference {
  if (typeof input !== 'string') {
    throw new HrefValidationError('invalid-uri');
  }

  const maxLength = options.maxLength ?? DEFAULT_MAX_HREF_LENGTH;
  if (!Number.isSafeInteger(maxLength) || maxLength < 1) {
    throw new TypeError('maxLength must be a positive safe integer');
  }

  const raw = input.replace(/^[\t\n\r ]+|[\t\n\r ]+$/g, '');
  if (raw.length === 0) {
    throw new HrefValidationError('empty');
  }
  if (raw.length > maxLength) {
    throw new HrefValidationError('too-long');
  }
  if (CONTROL_CHARACTER.test(raw)) {
    throw new HrefValidationError('control-character');
  }
  if (raw.includes('?') || raw.includes('#')) {
    throw new HrefValidationError('query-or-fragment');
  }

  if (raw.startsWith('/')) {
    if (raw.startsWith('//')) {
      throw new HrefValidationError('not-absolute');
    }
    return { raw, path: raw };
  }

  const absolute = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/([^/?#]*)([\s\S]*)$/.exec(raw);
  if (!absolute) {
    throw new HrefValidationError('not-absolute');
  }

  const scheme = absolute[1].toLowerCase();
  if (scheme !== 'http' && scheme !== 'https') {
    throw new HrefValidationError('invalid-uri');
  }

  const authority = absolute[2];
  if (authority.length === 0 || authority.includes('@')) {
    throw new HrefValidationError('invalid-uri');
  }

  const hrefAuthority = normalizeAuthority(authority, scheme);
  if (hrefAuthority === null) {
    throw new HrefValidationError('invalid-uri');
  }

  if (options.requestAuthority !== undefined) {
    const requestAuthority = normalizeRequestAuthority(options.requestAuthority);
    if (requestAuthority === null || requestAuthority !== hrefAuthority) {
      throw new HrefValidationError('foreign-authority');
    }
  } else if (!options.allowAnyAuthority) {
    throw new HrefValidationError('foreign-authority');
  }

  const path = absolute[3].length === 0 ? '/' : absolute[3];
  if (!path.startsWith('/')) {
    throw new HrefValidationError('invalid-uri');
  }

  return { raw, path, authority };
}

function parsePathSegments(
  path: string,
  kind: 'collection' | 'resource',
): ParsedPath {
  if (!path.startsWith('/')) {
    throw new HrefValidationError('not-absolute');
  }

  let candidate = path;
  if (kind === 'collection') {
    if (candidate === '/') {
      return { path: '/', segments: [] };
    }
    if (candidate.endsWith('/')) {
      candidate = candidate.slice(0, -1);
    }
  } else {
    if (candidate === '/' || candidate.endsWith('/')) {
      throw new HrefValidationError('not-a-member');
    }
  }

  const rawSegments = candidate.slice(1).split('/');
  if (rawSegments.some((segment) => segment.length === 0)) {
    throw new HrefValidationError('empty-segment');
  }

  const segments = rawSegments.map(decodeSegmentOnce);
  const encodedSegments = segments.map((segment) =>
    encodeURIComponent(segment),
  );

  return {
    path: `/${encodedSegments.join('/')}`,
    segments,
  };
}

/**
 * Percent-decode a segment exactly once. The decoded value is then checked for
 * path separators, dot segments, controls and NUL. A value such as
 * `%252e%252e` becomes `%2e%2e` and is treated as a literal segment name,
 * never as `..`.
 */
function decodeSegmentOnce(rawSegment: string): string {
  let decoded: string;
  try {
    decoded = decodeURIComponent(rawSegment);
  } catch {
    throw new HrefValidationError('bad-percent-encoding');
  }

  if (decoded === '.' || decoded === '..') {
    throw new HrefValidationError('dot-segment');
  }
  if (CONTROL_CHARACTER.test(decoded)) {
    throw new HrefValidationError('control-character');
  }
  if (PATH_SEPARATOR.test(decoded)) {
    throw new HrefValidationError('encoded-separator');
  }

  return decoded;
}

function normalizeAuthority(authority: string, scheme: string): string | null {
  try {
    return stripDefaultPort(new URL(`${scheme}://${authority}`).host.toLowerCase());
  } catch {
    return null;
  }
}

function normalizeRequestAuthority(value: string): string | null {
  if (typeof value !== 'string' || value.trim().length === 0) {
    return null;
  }

  try {
    const candidate = value.includes('://') ? value : `http://${value}`;
    return stripDefaultPort(new URL(candidate).host.toLowerCase());
  } catch {
    return null;
  }
}

function stripDefaultPort(host: string): string {
  return host.replace(/:(80|443)$/u, '');
}
