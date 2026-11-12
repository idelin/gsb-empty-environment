import type { ActivityPubObject, ActivityPubValue } from './types.ts';

export class UrlValidationError extends Error {
  readonly code: 'not_https' | 'has_userinfo' | 'unparseable' | 'missing_id';

  constructor(message: string, code: 'not_https' | 'has_userinfo' | 'unparseable' | 'missing_id') {
    super(message);
    this.name = 'UrlValidationError';
    this.code = code;
  }
}

/**
 * 解析并校验对象/活动 id：
 * - 必须是合法 URL；
 * - 协议必须是 https；
 * - 不允许携带 userinfo（https://user@host/...）。
 */
export function parseHttpsUrl(candidate: string): URL {
  let url: URL;
  try {
    url = new URL(candidate);
  } catch {
    throw new UrlValidationError(`invalid url: ${candidate}`, 'unparseable');
  }
  if (url.protocol !== 'https:') {
    throw new UrlValidationError(`object id must use https: ${candidate}`, 'not_https');
  }
  if (url.username !== '' || url.password !== '') {
    throw new UrlValidationError(`object id must not contain userinfo: ${candidate}`, 'has_userinfo');
  }
  return url;
}

/**
 * 从 HTTP 签名的 keyId 推导出其所属 actor。
 * keyId 形如 https://alice.example/actors/alice#main-key，
 * 去掉 fragment 后得到拥有该密钥的 actor 文档地址。
 */
export function actorFromKeyId(keyId: string): string {
  const url = parseHttpsUrl(keyId);
  url.hash = '';
  return url.href;
}

export function sameHost(left: string, right: string): boolean {
  const leftUrl = parseHttpsUrl(left);
  const rightUrl = parseHttpsUrl(right);
  return leftUrl.hostname.toLowerCase() === rightUrl.hostname.toLowerCase();
}

/** 读取字符串 id 或 { id } 形式对象的 id。 */
export function idOf(value: string | ActivityPubObject): string | undefined {
  if (typeof value === 'string') {
    return value;
  }
  return typeof value.id === 'string' ? value.id : undefined;
}

/** 从 attributedTo / actor 这类单值或数组字段中提取全部 id 字符串。 */
export function collectIds(value: ActivityPubValue): string[] {
  if (value == null) {
    return [];
  }
  if (typeof value === 'string') {
    return [value];
  }
  if (Array.isArray(value)) {
    return value.flatMap((entry) => collectIds(entry));
  }
  if (typeof value === 'object' && typeof value.id === 'string') {
    return [value.id];
  }
  return [];
}
