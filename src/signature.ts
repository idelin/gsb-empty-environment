import { createHmac } from 'node:crypto';

/** 确定性序列化：对象键排序，保证同一内容得到同一签名输入 */
export function canonicalize(value: unknown): string {
  if (Array.isArray(value)) {
    return '[' + value.map((item) => canonicalize(item)).join(',') + ']';
  }
  if (value !== null && typeof value === 'object') {
    const record = value as Record<string, unknown>;
    const body = Object.keys(record)
      .sort()
      .map((key) => JSON.stringify(key) + ':' + canonicalize(record[key]))
      .join(',');
    return '{' + body + '}';
  }
  return JSON.stringify(value) ?? 'null';
}

/** 责任人版本签名：HMAC-SHA256(责任人密钥, 规范化内容) */
export function signPayload(secret: string, payload: unknown): string {
  return createHmac('sha256', secret).update(canonicalize(payload)).digest('hex');
}
