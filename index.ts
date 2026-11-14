const ALGORITHM_STRENGTH = {
  sha1: 1,
  sha256: 2,
  sha384: 3,
  sha512: 4,
} as const;

const DIGEST_BYTES = {
  sha1: 20,
  sha256: 32,
  sha384: 48,
  sha512: 64,
} as const;

const WEB_CRYPTO_NAMES = {
  sha1: 'SHA-1',
  sha256: 'SHA-256',
  sha384: 'SHA-384',
  sha512: 'SHA-512',
} as const;

const MINIMUM_STRENGTH = ALGORITHM_STRENGTH.sha256;

export type SupportedHashAlgorithm = keyof typeof ALGORITHM_STRENGTH;
export type StrongHashAlgorithm = 'sha256' | 'sha384' | 'sha512';
export type IntegrityContent =
  | string
  | ArrayBuffer
  | ArrayBufferView
  | Blob;

export type IntegrityErrorCode =
  | 'MISSING_INTEGRITY'
  | 'INVALID_INTEGRITY'
  | 'UNSUPPORTED_ALGORITHM'
  | 'WEAK_ALGORITHM'
  | 'INTEGRITY_MISMATCH'
  | 'CRYPTO_UNAVAILABLE'
  | 'FETCH_FAILED'
  | 'INVALID_RESPONSE';

export interface IntegrityHash {
  readonly raw: string;
  readonly algorithm: SupportedHashAlgorithm;
  readonly digest: string;
  readonly digestBytes: Uint8Array;
  readonly strength: number;
  readonly weak: boolean;
  readonly options: readonly string[];
}

export interface IgnoredIntegrityHash {
  readonly raw: string;
  readonly algorithm: string;
  readonly reason: 'unsupported-algorithm';
}

export interface ParsedIntegrity {
  readonly raw: string;
  readonly hashes: readonly IntegrityHash[];
  readonly ignored: readonly IgnoredIntegrityHash[];
  readonly strongestAlgorithm: SupportedHashAlgorithm | null;
  readonly strongestHashes: readonly IntegrityHash[];
}

export interface IntegrityVerification {
  readonly valid: true;
  readonly algorithm: StrongHashAlgorithm;
  readonly digest: string;
  readonly matchedHash: IntegrityHash;
  readonly parsed: ParsedIntegrity;
}

export interface VerifyIntegrityOptions {
  /**
   * May raise, but cannot lower, the required strength. SHA-1 is parsed for
   * diagnostics, but is never accepted for verification.
   */
  readonly minimumStrength?: number;
}

export interface CachedContent {
  readonly key: string;
  readonly algorithm: StrongHashAlgorithm;
  readonly digest: string;
  readonly bytes: Uint8Array;
  readonly finalUrl: string;
  readonly contentType: string | null;
  readonly status?: number;
  readonly redirected?: boolean;
}

export interface VerifiedResource {
  readonly url: string;
  readonly finalUrl: string;
  readonly redirected: boolean;
  readonly crossOrigin: boolean;
  readonly status: number;
  readonly headers: Headers;
  readonly bytes: Uint8Array;
  readonly integrity: string;
  readonly algorithm: StrongHashAlgorithm;
  readonly digest: string;
  readonly contentKey: string;
  readonly cacheHit: boolean;
  text(): string;
}

export type FetchLike = (
  input: string | URL | Request,
  init?: RequestInit,
) => Promise<Response>;

export interface FetchVerifiedOptions
  extends Omit<RequestInit, 'redirect'> {
  readonly integrity?: string | null;
  readonly highRisk?: boolean;
  readonly requireIntegrity?: boolean;
  readonly cache?: ContentAddressedCache | null;
  readonly fetch?: FetchLike;
  readonly baseUrl?: string | URL;
  readonly redirect?: RequestRedirect;
}

interface IntegrityErrorDetails {
  readonly algorithm?: string;
  readonly url?: string;
  readonly expected?: string;
  readonly actual?: string;
}

export class IntegrityError extends Error {
  readonly code: IntegrityErrorCode;
  readonly algorithm?: string;
  readonly url?: string;
  readonly expected?: string;
  readonly actual?: string;

  constructor(
    code: IntegrityErrorCode,
    message: string,
    details: IntegrityErrorDetails = {},
  ) {
    super(message);
    this.name = 'IntegrityError';
    this.code = code;
    this.algorithm = details.algorithm;
    this.url = details.url;
    this.expected = details.expected;
    this.actual = details.actual;
  }
}

function isSupportedAlgorithm(
  algorithm: string,
): algorithm is SupportedHashAlgorithm {
  return Object.prototype.hasOwnProperty.call(ALGORITHM_STRENGTH, algorithm);
}

function normalizeAlgorithm(algorithm: string): string {
  return algorithm.trim().toLowerCase();
}

function decodeBase64(value: string): Uint8Array {
  const unpadded = value.replace(/=+$/, '');
  if (
    !unpadded ||
    unpadded.length % 4 === 1 ||
    !/^[A-Za-z0-9+/_-]+$/.test(unpadded)
  ) {
    throw new IntegrityError(
      'INVALID_INTEGRITY',
      `Invalid base64 digest: ${value}`,
    );
  }

  const alphabet =
    'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  const normalized = unpadded.replace(/-/g, '+').replace(/_/g, '/');
  const output: number[] = [];
  let accumulator = 0;
  let bits = 0;

  for (const character of normalized) {
    const valueIndex = alphabet.indexOf(character);
    if (valueIndex < 0) {
      throw new IntegrityError(
        'INVALID_INTEGRITY',
        `Invalid base64 digest: ${value}`,
      );
    }

    accumulator = ((accumulator << 6) | valueIndex) & 0xffffff;
    bits += 6;

    if (bits >= 8) {
      bits -= 8;
      output.push((accumulator >>> bits) & 0xff);
    }
  }

  if (bits > 0 && (accumulator & ((1 << bits) - 1)) !== 0) {
    throw new IntegrityError(
      'INVALID_INTEGRITY',
      `Non-zero trailing bits in digest: ${value}`,
    );
  }

  return Uint8Array.from(output);
}

function encodeBase64(bytes: Uint8Array): string {
  const alphabet =
    'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  let output = '';

  for (let index = 0; index < bytes.length; index += 3) {
    const first = bytes[index];
    const second = index + 1 < bytes.length ? bytes[index + 1] : 0;
    const third = index + 2 < bytes.length ? bytes[index + 2] : 0;
    const chunk = (first << 16) | (second << 8) | third;

    output += alphabet[(chunk >>> 18) & 63];
    output += alphabet[(chunk >>> 12) & 63];
    output += index + 1 < bytes.length ? alphabet[(chunk >>> 6) & 63] : '=';
    output += index + 2 < bytes.length ? alphabet[chunk & 63] : '=';
  }

  return output;
}

function constantTimeEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.length !== right.length) {
    return false;
  }

  let difference = 0;
  for (let index = 0; index < left.length; index += 1) {
    difference |= left[index] ^ right[index];
  }
  return difference === 0;
}

function parseHashToken(
  rawToken: string,
): IntegrityHash | IgnoredIntegrityHash {
  const separator = rawToken.indexOf('-');
  if (separator <= 0 || separator === rawToken.length - 1) {
    throw new IntegrityError(
      'INVALID_INTEGRITY',
      `Malformed integrity token: ${rawToken}`,
    );
  }

  const rawAlgorithm = rawToken.slice(0, separator);
  const remainder = rawToken.slice(separator + 1);
  const optionStart = remainder.indexOf('?');
  const digest = optionStart < 0 ? remainder : remainder.slice(0, optionStart);
  const options =
    optionStart < 0
      ? []
      : remainder
          .slice(optionStart + 1)
          .split('?')
          .filter((option) => option.length > 0);
  const algorithm = normalizeAlgorithm(rawAlgorithm);

  if (!isSupportedAlgorithm(algorithm)) {
    return {
      raw: rawToken,
      algorithm,
      reason: 'unsupported-algorithm',
    };
  }

  const digestBytes = decodeBase64(digest);
  if (digestBytes.length !== DIGEST_BYTES[algorithm]) {
    throw new IntegrityError(
      'INVALID_INTEGRITY',
      `Digest length for ${algorithm} is ${digestBytes.length}, expected ${DIGEST_BYTES[algorithm]}`,
      { algorithm },
    );
  }

  return {
    raw: rawToken,
    algorithm,
    digest,
    digestBytes,
    strength: ALGORITHM_STRENGTH[algorithm],
    weak: ALGORITHM_STRENGTH[algorithm] < MINIMUM_STRENGTH,
    options: Object.freeze(options),
  };
}

/**
 * Parses every token in an SRI integrity attribute. Returned hashes are sorted
 * strongest-first. Unsupported algorithms are reported, not promoted to the
 * strongest slot.
 */
export function parseIntegrity(
  integrity: string | null | undefined,
): ParsedIntegrity {
  const raw = typeof integrity === 'string' ? integrity : '';
  const tokens = raw.trim().length === 0 ? [] : raw.trim().split(/\s+/);
  const hashes: IntegrityHash[] = [];
  const ignored: IgnoredIntegrityHash[] = [];

  for (const token of tokens) {
    const parsed = parseHashToken(token);
    if ('reason' in parsed) {
      ignored.push(parsed);
    } else {
      hashes.push(parsed);
    }
  }

  hashes.sort((left, right) => right.strength - left.strength);

  const strongestAlgorithm = hashes.length > 0 ? hashes[0].algorithm : null;
  const strongestHashes =
    strongestAlgorithm === null
      ? []
      : hashes.filter((hash) => hash.algorithm === strongestAlgorithm);

  return {
    raw,
    hashes: Object.freeze(hashes),
    ignored: Object.freeze(ignored),
    strongestAlgorithm,
    strongestHashes: Object.freeze(strongestHashes),
  };
}

function getSubtleCrypto(): SubtleCrypto {
  const webCrypto = (
    globalThis as typeof globalThis & {
      crypto?: { subtle?: SubtleCrypto };
    }
  ).crypto;

  if (!webCrypto?.subtle) {
    throw new IntegrityError(
      'CRYPTO_UNAVAILABLE',
      'Web Crypto is required to verify subresource integrity',
    );
  }

  return webCrypto.subtle;
}

async function contentToBytes(content: IntegrityContent): Promise<Uint8Array> {
  if (typeof content === 'string') {
    return new TextEncoder().encode(content);
  }

  if (content instanceof ArrayBuffer) {
    return new Uint8Array(content);
  }

  if (ArrayBuffer.isView(content)) {
    return new Uint8Array(
      content.buffer,
      content.byteOffset,
      content.byteLength,
    );
  }

  if (typeof Blob !== 'undefined' && content instanceof Blob) {
    return new Uint8Array(await content.arrayBuffer());
  }

  throw new TypeError(
    'Integrity content must be a string, buffer, view, or Blob',
  );
}

async function digestBytes(
  content: IntegrityContent,
  algorithm: SupportedHashAlgorithm,
): Promise<Uint8Array> {
  const bytes = await contentToBytes(content);
  const digest = await getSubtleCrypto().digest(
    WEB_CRYPTO_NAMES[algorithm],
    bytes,
  );
  return new Uint8Array(digest);
}

export async function computeDigest(
  content: IntegrityContent,
  algorithm: SupportedHashAlgorithm | string = 'sha384',
): Promise<string> {
  const normalized = normalizeAlgorithm(algorithm);
  if (!isSupportedAlgorithm(normalized)) {
    throw new IntegrityError(
      'UNSUPPORTED_ALGORITHM',
      `Unsupported hash algorithm: ${algorithm}`,
      { algorithm: normalized },
    );
  }

  return encodeBase64(await digestBytes(content, normalized));
}

/**
 * Build-time helper. It intentionally refuses SHA-1 because emitting a weak
 * digest would make the generated markup vulnerable to a weak-hash fallback.
 */
export async function generateIntegrity(
  content: IntegrityContent,
  algorithms: string | readonly string[] = 'sha384',
): Promise<string> {
  const requested: readonly string[] =
    typeof algorithms === 'string' ? [algorithms] : algorithms;
  if (requested.length === 0) {
    throw new IntegrityError(
      'UNSUPPORTED_ALGORITHM',
      'At least one hash algorithm is required',
    );
  }

  const unique = Array.from(
    new Set(requested.map((algorithm) => normalizeAlgorithm(algorithm))),
  );
  const normalized: StrongHashAlgorithm[] = [];
  for (const algorithm of unique) {
    if (!isSupportedAlgorithm(algorithm)) {
      throw new IntegrityError(
        'UNSUPPORTED_ALGORITHM',
        `Unsupported integrity algorithm: ${algorithm}`,
        { algorithm },
      );
    }

    if (ALGORITHM_STRENGTH[algorithm] >= MINIMUM_STRENGTH) {
      normalized.push(algorithm as StrongHashAlgorithm);
    }
  }

  if (normalized.length === 0) {
    throw new IntegrityError(
      'WEAK_ALGORITHM',
      'Refusing to generate an integrity value using only weak algorithms',
    );
  }

  normalized.sort(
    (left, right) =>
      ALGORITHM_STRENGTH[right] - ALGORITHM_STRENGTH[left],
  );

  const hashes = await Promise.all(
    normalized.map(async (algorithm) => {
      const digest = await computeDigest(content, algorithm);
      return `${algorithm}-${digest}`;
    }),
  );

  return hashes.join(' ');
}

/**
 * Applies the minimum-strength policy without allowing callers to lower it to
 * SHA-1.
 */
function selectStrongestAlgorithm(
  parsed: ParsedIntegrity,
  requestedMinimumStrength?: number,
): StrongHashAlgorithm {
  if (parsed.hashes.length === 0) {
    throw new IntegrityError(
      'UNSUPPORTED_ALGORITHM',
      'Integrity metadata contains no supported hash algorithm',
    );
  }

  const minimumStrength =
    typeof requestedMinimumStrength === 'number' &&
    Number.isFinite(requestedMinimumStrength)
      ? Math.max(requestedMinimumStrength, MINIMUM_STRENGTH)
      : MINIMUM_STRENGTH;

  if (
    parsed.strongestAlgorithm === null ||
    ALGORITHM_STRENGTH[parsed.strongestAlgorithm] < minimumStrength
  ) {
    throw new IntegrityError(
      'WEAK_ALGORITHM',
      `The strongest declared algorithm is ${parsed.strongestAlgorithm ?? 'unknown'}; SHA-1 is not an acceptable fallback`,
      { algorithm: parsed.strongestAlgorithm ?? undefined },
    );
  }

  return parsed.strongestAlgorithm as StrongHashAlgorithm;
}

/**
 * Verifies content against the strongest declared algorithm. A matching SHA-1
 * digest can never rescue a missing or mismatching SHA-256+ digest.
 */
export async function verifyIntegrity(
  content: IntegrityContent,
  integrity: string | null | undefined,
  options: VerifyIntegrityOptions = {},
): Promise<IntegrityVerification> {
  if (typeof integrity !== 'string' || integrity.trim().length === 0) {
    throw new IntegrityError(
      'MISSING_INTEGRITY',
      'No integrity metadata was supplied',
    );
  }

  const parsed = parseIntegrity(integrity);
  const algorithm = selectStrongestAlgorithm(parsed, options.minimumStrength);
  const actualBytes = await digestBytes(content, algorithm);
  const actual = encodeBase64(actualBytes);
  const matchedHash = parsed.strongestHashes.find((hash) =>
    constantTimeEqual(hash.digestBytes, actualBytes),
  );

  if (!matchedHash) {
    throw new IntegrityError(
      'INTEGRITY_MISMATCH',
      `The strongest declared digest (${algorithm}) does not match the content`,
      {
        algorithm,
        expected: parsed.strongestHashes.map((hash) => hash.digest).join(' '),
        actual,
      },
    );
  }

  return {
    valid: true,
    algorithm,
    digest: actual,
    matchedHash,
    parsed,
  };
}

function contentKey(algorithm: StrongHashAlgorithm, digest: string): string {
  return `${algorithm}-${digest}`;
}

/**
 * Content-addressed cache. The map key is always a digest of the exact bytes,
 * while URL aliases only accelerate lookup. Every cache hit is re-verified
 * against the current integrity attribute before it can be returned.
 */
export class ContentAddressedCache {
  private entries = new Map<string, CachedContent>();
  private aliases = new Map<string, string>();

  get size(): number {
    return this.entries.size;
  }

  get(key: string): CachedContent | undefined {
    return this.entries.get(key);
  }

  getByDigest(
    algorithm: StrongHashAlgorithm,
    digest: string,
  ): CachedContent | undefined {
    return this.entries.get(contentKey(algorithm, digest));
  }

  resolve(url: string): CachedContent | undefined {
    const key = this.aliases.get(this.normalizeUrl(url));
    return key === undefined ? undefined : this.entries.get(key);
  }

  store(entry: CachedContent, urls: readonly string[] = []): void {
    const expectedKey = contentKey(entry.algorithm, entry.digest);
    if (entry.key !== expectedKey) {
      throw new IntegrityError(
        'INVALID_INTEGRITY',
        `Cache key ${entry.key} does not match its content digest ${expectedKey}`,
        { algorithm: entry.algorithm },
      );
    }

    const stableEntry: CachedContent = Object.freeze({
      ...entry,
      bytes: Uint8Array.from(entry.bytes),
    });
    this.entries.set(entry.key, stableEntry);

    for (const url of [entry.finalUrl, ...urls]) {
      if (url.length > 0) {
        this.aliases.set(this.normalizeUrl(url), entry.key);
      }
    }
  }

  clear(): void {
    this.entries.clear();
    this.aliases.clear();
  }

  private normalizeUrl(url: string): string {
    try {
      return new URL(url).href;
    } catch {
      return url;
    }
  }
}

const HIGH_RISK_PATH = /\.(?:m?js|cjs|css|html?|wasm)$/i;

function resolveRequestUrl(
  input: string | URL | Request,
  baseUrl?: string | URL,
): string {
  const rawUrl =
    typeof input === 'string'
      ? input
      : input instanceof URL
        ? input.href
        : input.url;

  const base = baseUrl ?? getDocumentHref();
  if (base !== undefined) {
    try {
      return new URL(rawUrl, base).href;
    } catch {
      return rawUrl;
    }
  }

  return rawUrl;
}

function getDocumentHref(): string | undefined {
  const candidate = (
    globalThis as typeof globalThis & {
      location?: { href?: string };
    }
  ).location;
  return typeof candidate?.href === 'string' ? candidate.href : undefined;
}

function isCrossOriginResource(
  requestedUrl: string,
  finalUrl: string,
  baseUrl?: string | URL,
): boolean {
  try {
    const documentUrl = baseUrl ?? getDocumentHref();
    const referenceOrigin =
      documentUrl === undefined
        ? new URL(requestedUrl).origin
        : new URL(documentUrl).origin;
    return new URL(finalUrl).origin !== referenceOrigin;
  } catch {
    return false;
  }
}

function isHighRiskUrl(url: string): boolean {
  try {
    return HIGH_RISK_PATH.test(new URL(url).pathname);
  } catch {
    return HIGH_RISK_PATH.test(url.split(/[?#]/, 1)[0]);
  }
}

function appendIntegrityAliases(
  cache: ContentAddressedCache,
  entry: CachedContent,
  urls: readonly string[],
): void {
  cache.store(entry, urls);
}

/**
 * Fetches a resource, hashes the final response body after all redirects, and
 * refuses to return it unless the strongest declared digest matches.
 */
export async function fetchVerifiedResource(
  input: string | URL | Request,
  options: FetchVerifiedOptions = {},
): Promise<VerifiedResource> {
  const {
    integrity,
    highRisk,
    requireIntegrity,
    cache = null,
    fetch: customFetch,
    baseUrl,
    redirect,
    ...requestOptions
  } = options;

  const requestedUrl = resolveRequestUrl(input, baseUrl);
  const inferredHighRisk = highRisk ?? isHighRiskUrl(requestedUrl);
  const mustHaveIntegrity = inferredHighRisk || requireIntegrity !== false;
  const hasIntegrity =
    typeof integrity === 'string' && integrity.trim().length > 0;

  if (!hasIntegrity) {
    throw new IntegrityError(
      'MISSING_INTEGRITY',
      mustHaveIntegrity
        ? `High-risk resource ${requestedUrl} has no integrity attribute`
        : `Resource ${requestedUrl} cannot be returned as verified without integrity metadata`,
      { url: requestedUrl },
    );
  }

  const integrityValue = integrity as string;
  const parsedIntegrity = parseIntegrity(integrityValue);
  const strongestAlgorithm = selectStrongestAlgorithm(parsedIntegrity);

  if (cache !== null) {
    let cached =
      cache.resolve(requestedUrl) ??
      cache.resolve(
        typeof input === 'string'
          ? input
          : input instanceof URL
            ? input.href
            : '',
      );

    if (cached === undefined) {
      for (const hash of parsedIntegrity.strongestHashes) {
        cached = cache.getByDigest(
          strongestAlgorithm,
          encodeBase64(hash.digestBytes),
        );
        if (cached !== undefined) {
          break;
        }
      }
    }

    if (cached !== undefined) {
      try {
        const verification = await verifyIntegrity(cached.bytes, integrityValue);
        const key = contentKey(verification.algorithm, verification.digest);
        const refreshedEntry: CachedContent =
          key === cached.key
            ? cached
            : {
                key,
                algorithm: verification.algorithm,
                digest: verification.digest,
                bytes: cached.bytes,
                finalUrl: cached.finalUrl,
                contentType: cached.contentType,
                status: cached.status,
                redirected: cached.redirected,
              };

        appendIntegrityAliases(cache, refreshedEntry, [requestedUrl]);

        return {
          url: requestedUrl,
          finalUrl: cached.finalUrl,
          redirected: cached.redirected === true,
          crossOrigin: isCrossOriginResource(
            requestedUrl,
            cached.finalUrl,
            baseUrl,
          ),
          status: cached.status ?? 200,
          headers: new Headers({
            'content-type': cached.contentType ?? 'application/octet-stream',
          }),
          bytes: Uint8Array.from(cached.bytes),
          integrity: integrityValue,
          algorithm: verification.algorithm,
          digest: verification.digest,
          contentKey: key,
          cacheHit: true,
          text: () => new TextDecoder().decode(cached.bytes),
        };
      } catch {
        // A stale alias must never become an integrity bypass.
      }
    }
  }

  const fetchImpl =
    customFetch ??
    (typeof globalThis.fetch === 'function'
      ? globalThis.fetch.bind(globalThis)
      : undefined);
  if (fetchImpl === undefined) {
    throw new IntegrityError(
      'FETCH_FAILED',
      'No Fetch API implementation is available',
      { url: requestedUrl },
    );
  }

  const fetchTarget =
    typeof input === 'string' || input instanceof URL ? requestedUrl : input;
  const response = await fetchImpl(fetchTarget, {
    ...requestOptions,
    redirect: redirect ?? 'follow',
  });

  if (
    response.type === 'opaque' ||
    response.type === 'opaqueredirect' ||
    response.ok === false
  ) {
    throw new IntegrityError(
      'INVALID_RESPONSE',
      `Fetching ${requestedUrl} failed with status ${response.status}`,
      { url: requestedUrl },
    );
  }

  const contentType = response.headers?.get('content-type') ?? null;
  const responseUrl = typeof response.url === 'string' ? response.url : '';
  const finalUrl = responseUrl.length > 0 ? responseUrl : requestedUrl;
  const bytes = new Uint8Array(await response.arrayBuffer());
  const verification = await verifyIntegrity(bytes, integrityValue);
  const key = contentKey(verification.algorithm, verification.digest);

  if (cache !== null) {
    cache.store(
      {
        key,
        algorithm: verification.algorithm,
        digest: verification.digest,
        bytes,
        finalUrl,
        contentType,
        status: typeof response.status === 'number' ? response.status : 200,
        redirected:
          response.redirected === true || finalUrl !== requestedUrl,
      },
      [requestedUrl],
    );
  }

  return {
    url: requestedUrl,
    finalUrl,
    redirected: response.redirected === true || finalUrl !== requestedUrl,
    crossOrigin: isCrossOriginResource(requestedUrl, finalUrl, baseUrl),
    status: typeof response.status === 'number' ? response.status : 200,
    headers: response.headers,
    bytes,
    integrity: integrityValue,
    algorithm: verification.algorithm,
    digest: verification.digest,
    contentKey: key,
    cacheHit: false,
    text: () => new TextDecoder().decode(bytes),
  };
}

export const verifySubresourceIntegrity = verifyIntegrity;
export const fetchWithIntegrity = fetchVerifiedResource;
export const createSubresourceIntegrity = generateIntegrity;

export interface IntegrityVerifier {
  parse: typeof parseIntegrity;
  digest: typeof computeDigest;
  generate: typeof generateIntegrity;
  verify: typeof verifyIntegrity;
  fetch: typeof fetchVerifiedResource;
}

export function createIntegrityVerifier(): IntegrityVerifier {
  return {
    parse: parseIntegrity,
    digest: computeDigest,
    generate: generateIntegrity,
    verify: verifyIntegrity,
    fetch: fetchVerifiedResource,
  };
}

export default createIntegrityVerifier;
