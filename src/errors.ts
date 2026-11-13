/**
 * 统一的校验错误类型。
 *
 * 设计原则：
 * - 访问密钥（UAK）的所有失败路径抛出同一个 {@link AccessKeyError}，
 *   调用方只能返回固定错误，避免通过错误文案/类型枚举收件人或探测密钥。
 * - 证书失败按“是否需要静默丢弃”区分：过期（含未生效）使用
 *   {@link CertificateExpiredError}，其余结构/信任/绑定问题使用
 *   {@link CertificateError}。
 */

/** 服务端校验 Unidentified-Access-Key 失败时抛出的唯一错误。 */
export class AccessKeyError extends Error {
  public readonly code = "ACCESS_KEY_REJECTED" as const;

  constructor(message = "unidentified access key rejected") {
    super(message);
    this.name = "AccessKeyError";
  }
}

export type CertificateErrorCode =
  | "CERT_MALFORMED"
  | "CERT_UNTRUSTED_ROOT"
  | "CERT_EXPIRED"
  | "CERT_IDENTITY_MISMATCH"
  | "CERT_SENDER_MISMATCH";

/** 客户端侧发送者证书校验失败的基类错误。 */
export class CertificateError extends Error {
  public readonly code: CertificateErrorCode;

  constructor(code: CertificateErrorCode, message: string) {
    super(message);
    this.name = "CertificateError";
    this.code = code;
  }
}

/**
 * 证书超出有效期（含尚未生效，时钟容差后仍不满足）。
 *
 * 该错误必须导致消息仅被丢弃、不发送任何回执，因此单独建模，
 * 便于调用方用 {@link isCertificateExpiredError} 精确识别。
 */
export class CertificateExpiredError extends CertificateError {
  constructor(message = "sender certificate is not within its validity period") {
    super("CERT_EXPIRED", message);
    this.name = "CertificateExpiredError";
  }
}

export function isCertificateExpiredError(
  value: unknown,
): value is CertificateExpiredError {
  return (
    value instanceof CertificateExpiredError ||
    (value instanceof CertificateError && value.code === "CERT_EXPIRED")
  );
}
