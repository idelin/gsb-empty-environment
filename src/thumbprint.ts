/**
 * 按 RFC 7638 计算 JWK 公钥指纹（SHA-256，base64url，无填充）。
 *
 * 指纹只由公钥参数构成，因此可用于比较令牌头自带 jwk 与登记表公钥
 * 是否为同一把公钥，而无需信任头里的其他字段（含私钥字段也会被忽略）。
 */

const CANONICAL_MEMBERS: Record<string, string[]> = {
  EC: ['crv', 'kty', 'x', 'y'],
  RSA: ['e', 'kty', 'n'],
  OKP: ['crv', 'kty', 'x'],
};

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0;
}

/**
 * 提取指纹所需的规范成员；缺字段或 kty 不受支持时抛错，
 * 调用方应把这种令牌按非法/伪造处理。
 */
export function canonicalJwkObject(jwk: JsonWebKey): Record<string, string> {
  if (!jwk || typeof jwk !== 'object' || !isNonEmptyString(jwk.kty)) {
    throw new Error('jwk must be an object with a string kty');
  }
  const members = CANONICAL_MEMBERS[jwk.kty];
  if (!members) {
    throw new Error(`unsupported jwk kty: ${String(jwk.kty)}`);
  }
  const canonical: Record<string, string> = {};
  for (const member of members) {
    const value = (jwk as Record<string, unknown>)[member];
    if (!isNonEmptyString(value)) {
      throw new Error(`jwk is missing required member: ${member}`);
    }
    canonical[member] = value;
  }
  return canonical;
}

export async function jwkThumbprint(jwk: JsonWebKey): Promise<string> {
  const canonical = canonicalJwkObject(jwk);
  const serialized = JSON.stringify(canonical);
  const digest = await crypto.subtle.digest(
    'SHA-256',
    new TextEncoder().encode(serialized),
  );
  const bytes = new Uint8Array(digest);
  let base64url = '';
  for (const byte of bytes) {
    base64url += String.fromCharCode(byte);
  }
  return btoa(base64url).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}
