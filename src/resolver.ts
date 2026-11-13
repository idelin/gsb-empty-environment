import { jwkThumbprint } from './thumbprint.ts';
import { checkKeyUrl } from './url-policy.ts';
import type {
  JwsHeaderLike,
  KeyRegistry,
  KeySourceDecision,
  KeySourceRejection,
  RegisteredKeyEntry,
} from './types.ts';

function reject(
  reason: KeySourceRejection['reason'],
  detail: string,
): KeySourceRejection {
  return { kind: 'reject', reason, detail };
}

function isJwk(value: unknown): value is JsonWebKey {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * 验签前的密钥来源判定。该函数不发起任何网络请求：
 *
 * 1. `kid` 必须存在且在登记表内，否则拒绝且不触发抓取；
 * 2. `alg` 必须在该来源登记的算法集合内；
 * 3. 头里的 `jwk` 只用于比对：与登记公钥指纹不同即伪造令牌，
 *    相同也仍然只用登记表里的公钥；`x5c` 永远被忽略；
 * 4. `jku` / `x5u` 只有通过来源策略检查后才返回 `fetch-url` 决定，
 *    实际抓取交给 {@link fetchKeyMaterial}，且必须禁止重定向、限制 64 KiB。
 */
export async function resolveKeySource(
  header: JwsHeaderLike,
  registry: KeyRegistry,
): Promise<KeySourceDecision> {
  const kid = header.kid;
  if (typeof kid !== 'string' || kid.length === 0) {
    return reject('missing_kid', 'JWS header has no usable kid');
  }
  const entry: RegisteredKeyEntry | undefined = registry[kid];
  if (!entry) {
    return reject('unknown_kid', `kid ${kid} is not registered; no key material will be fetched`);
  }

  const alg = header.alg;
  if (typeof alg !== 'string' || alg.length === 0) {
    return reject('missing_alg', 'JWS header has no usable alg');
  }
  if (!entry.allowedAlgs.includes(alg)) {
    return reject(
      'alg_not_allowed',
      `alg ${alg} is not in the allowed set [${entry.allowedAlgs.join(', ')}] for kid ${kid}`,
    );
  }

  if (header.jwk !== undefined) {
    if (!isJwk(header.jwk)) {
      return reject('invalid_header_jwk', 'header jwk is not a JSON object');
    }
    let headerThumbprint: string;
    let registeredThumbprint: string;
    try {
      headerThumbprint = await jwkThumbprint(header.jwk);
      registeredThumbprint = await jwkThumbprint(entry.publicJwk);
    } catch (error) {
      return reject(
        'invalid_header_jwk',
        `cannot compute JWK thumbprint: ${error instanceof Error ? error.message : String(error)}`,
      );
    }
    if (headerThumbprint !== registeredThumbprint) {
      return reject(
        'forged_jwk',
        'header jwk public key differs from the registered public key for this kid',
      );
    }
  }

  const hasJku = header.jku !== undefined;
  const hasX5u = header.x5u !== undefined;
  if (hasJku && hasX5u) {
    return reject('multiple_key_urls', 'both jku and x5u are present; the key source is ambiguous');
  }

  if (hasJku) {
    const result = checkKeyUrl(header.jku, entry);
    if (!result.ok || !result.url) {
      return reject('invalid_jku', `${result.violation}: ${result.detail ?? ''}`.trim());
    }
    return {
      kind: 'fetch-url',
      kid,
      alg,
      header: 'jku',
      url: result.url.toString(),
      entry,
    };
  }

  if (hasX5u) {
    const result = checkKeyUrl(header.x5u, entry);
    if (!result.ok || !result.url) {
      return reject('invalid_x5u', `${result.violation}: ${result.detail ?? ''}`.trim());
    }
    return {
      kind: 'fetch-url',
      kid,
      alg,
      header: 'x5u',
      url: result.url.toString(),
      entry,
    };
  }

  return {
    kind: 'use-registered-key',
    kid,
    alg,
    publicJwk: entry.publicJwk,
    entry,
  };
}
