import { AccessKeyError } from "./errors.ts";
import { timingSafeEqualBytes } from "./timing.ts";

/** Signal Unidentified-Access-Key 的固定长度（16 字节）。 */
export const ACCESS_KEY_LENGTH = 16;

function isBytes(value: unknown): value is Uint8Array {
  return value instanceof Uint8Array;
}

function decodeHex(value: string): Uint8Array {
  if (value.length !== ACCESS_KEY_LENGTH * 2 || !/^[0-9a-fA-F]+$/.test(value)) {
    throw new AccessKeyError();
  }
  const bytes = new Uint8Array(ACCESS_KEY_LENGTH);
  for (let index = 0; index < ACCESS_KEY_LENGTH; index += 1) {
    bytes[index] = Number.parseInt(value.slice(index * 2, index * 2 + 2), 16);
  }
  return bytes;
}

function decodeBase64(value: string): Uint8Array {
  let bytes: Uint8Array;
  try {
    const binary = atob(value);
    bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
  } catch {
    throw new AccessKeyError();
  }
  if (bytes.length !== ACCESS_KEY_LENGTH) {
    throw new AccessKeyError();
  }
  return bytes;
}

/**
 * 将调用方提供的访问密钥归一化为原始 16 字节。
 * 接受 Uint8Array、hex 或 base64；任何畸形输入都得到同一个 {@link AccessKeyError}。
 */
export function normalizeAccessKey(candidate: unknown): Uint8Array {
  if (isBytes(candidate)) {
    if (candidate.length !== ACCESS_KEY_LENGTH) {
      throw new AccessKeyError();
    }
    return candidate;
  }
  if (typeof candidate !== "string") {
    throw new AccessKeyError();
  }
  if (/^[0-9a-fA-F]+$/.test(candidate)) {
    return decodeHex(candidate);
  }
  return decodeBase64(candidate);
}

/** 收件人登记记录：服务端存储的访问密钥与可选收件人标识。 */
export interface AccessKeyRecord {
  recipientId: string;
  accessKey: Uint8Array;
}

/** 收件人查询钩子，由接入方实现（数据库、缓存等）。 */
export type AccessKeyRegistry = (
  recipientId: string,
) => Promise<AccessKeyRecord | undefined>;

export interface VerifyUnidentifiedAccessKeyInput {
  /** 请求头 Unidentified-Access-Key 中携带的值。 */
  presentedKey: unknown;
  /** 目标收件人标识（密封消息外层信封的收件人）。 */
  recipientId: string;
  registry: AccessKeyRegistry;
}

function randomDummyKey(): Uint8Array {
  const dummy = new Uint8Array(ACCESS_KEY_LENGTH);
  globalThis.crypto.getRandomValues(dummy);
  return dummy;
}

/**
 * 服务端校验 Unidentified-Access-Key。
 *
 * 安全特性：
 * - 登记值与提交值使用 {@link timingSafeEqualBytes} 做常数时间比较；
 * - 收件人不存在、未登记密钥、密钥畸形、密钥不匹配全部抛出同一个
 *   {@link AccessKeyError}，调用方应映射为同一个 401/拒绝响应；
 * - 收件人缺失时仍生成随机假密钥并执行一次比较，抹平分支与计时差异，
 *   防止收件人枚举。
 */
export async function verifyUnidentifiedAccessKey(
  input: VerifyUnidentifiedAccessKeyInput,
): Promise<AccessKeyRecord> {
  const record = await input.registry(input.recipientId);
  const registeredKey = record?.accessKey ?? randomDummyKey();

  let presented: Uint8Array;
  try {
    presented = normalizeAccessKey(input.presentedKey);
  } catch (error) {
    if (error instanceof AccessKeyError) {
      timingSafeEqualBytes(registeredKey, randomDummyKey());
      throw new AccessKeyError();
    }
    throw error;
  }

  const matched = timingSafeEqualBytes(registeredKey, presented);
  if (!record || !matched) {
    throw new AccessKeyError();
  }
  return record;
}
