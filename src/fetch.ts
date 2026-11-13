import type { FetchedKeyMaterial, JwkSet } from './types.ts';

export const MAX_KEY_RESPONSE_BYTES = 64 * 1024;

export type KeyFetchErrorReason =
  | 'not_https'
  | 'redirect_blocked'
  | 'http_error'
  | 'response_too_large'
  | 'fetch_failed';

export class KeyFetchError extends Error {
  readonly reason: KeyFetchErrorReason;

  constructor(reason: KeyFetchErrorReason, message: string) {
    super(message);
    this.name = 'KeyFetchError';
    this.reason = reason;
  }
}

export interface FetchKeyOptions {
  /** 注入自定义 fetch（主要用于测试）；默认使用全局 fetch。 */
  fetch?: typeof fetch;
  /** 透传给 fetch 的额外选项（redirect 会被强制为 'manual'）。 */
  init?: Omit<RequestInit, 'redirect' | 'credentials'>;
  /** 允许的最大响应字节数，默认 64 KiB。 */
  maxBytes?: number;
}

/**
 * 按来源决定抓取 jku/x5u：
 *
 * - 仅允许 https；
 * - `redirect: 'manual'`，任何 3xx 重定向响应都直接拒绝（不跟随）；
 * - 响应体超过 `maxBytes`（默认 64 KiB）即拒绝；
 * - 不携带任何凭据（cookie / TLS 客户端凭据）。
 */
export async function fetchKeyMaterial(
  url: string,
  options: FetchKeyOptions = {},
): Promise<FetchedKeyMaterial> {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    throw new KeyFetchError('fetch_failed', `invalid URL: ${url}`);
  }
  if (parsed.protocol !== 'https:') {
    throw new KeyFetchError('not_https', `refusing to fetch non-https URL: ${url}`);
  }

  const doFetch = options.fetch ?? fetch;
  let response: Response;
  try {
    response = await doFetch(url, {
      ...options.init,
      redirect: 'manual',
      credentials: 'omit',
    });
  } catch (error) {
    throw new KeyFetchError(
      'fetch_failed',
      `network error while fetching ${url}: ${error instanceof Error ? error.message : String(error)}`,
    );
  }

  if (response.type === 'opaqueredirect' || (response.status >= 300 && response.status < 400)) {
    throw new KeyFetchError(
      'redirect_blocked',
      `refusing to follow a redirect (status ${response.status}, type ${response.type}) for ${url}`,
    );
  }
  if (!response.ok) {
    throw new KeyFetchError('http_error', `unexpected HTTP ${response.status} for ${url}`);
  }

  const maxBytes = options.maxBytes ?? MAX_KEY_RESPONSE_BYTES;
  const declaredLength = Number(response.headers.get('content-length') ?? NaN);
  if (Number.isFinite(declaredLength) && declaredLength > maxBytes) {
    throw new KeyFetchError(
      'response_too_large',
      `content-length ${declaredLength} exceeds limit of ${maxBytes} bytes for ${url}`,
    );
  }

  if (!response.body) {
    const buffer = new Uint8Array(await response.arrayBuffer());
    if (buffer.byteLength > maxBytes) {
      throw new KeyFetchError(
        'response_too_large',
        `response of ${buffer.byteLength} bytes exceeds limit of ${maxBytes} bytes for ${url}`,
      );
    }
    return { url, contentType: response.headers.get('content-type'), bytes: buffer };
  }

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    total += value.byteLength;
    if (total > maxBytes) {
      await reader.cancel().catch(() => undefined);
      throw new KeyFetchError(
        'response_too_large',
        `response exceeds limit of ${maxBytes} bytes for ${url}`,
      );
    }
    chunks.push(value);
  }

  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return { url, contentType: response.headers.get('content-type'), bytes };
}

/** 解析 JWK Set 响应并按 kid 选取密钥；找不到时返回 null。 */
export function parseJwkSet(material: FetchedKeyMaterial, kid: string): JsonWebKey | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(new TextDecoder().decode(material.bytes));
  } catch (error) {
    throw new KeyFetchError(
      'fetch_failed',
      `JWK Set is not valid JSON: ${error instanceof Error ? error.message : String(error)}`,
    );
  }
  if (
    typeof parsed !== 'object' ||
    parsed === null ||
    !Array.isArray((parsed as JwkSet).keys)
  ) {
    throw new KeyFetchError('fetch_failed', 'JWK Set response has no "keys" array');
  }
  const set = parsed as JwkSet;
  return set.keys.find((key) => key && key.kid === kid) ?? null;
}
