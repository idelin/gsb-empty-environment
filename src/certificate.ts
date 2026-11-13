import { defaultSignatureVerifier, type SignatureVerifier } from "./crypto.ts";
import {
  CertificateError,
  CertificateExpiredError,
} from "./errors.ts";
import { timingSafeEqualBytes, timingSafeEqualString } from "./timing.ts";

/** 时钟偏差容差：5 分钟（毫秒）。 */
export const CERTIFICATE_CLOCK_SKEW_MS = 5 * 60 * 1000;

/**
 * 发送者证书。
 *
 * {@link signedData} 是被签名的规范化字节（例如 protobuf 编码的证书负载），
 * {@link signature} 是受信根对该负载的签名。接入方按自己的序列化格式生成，
 * 校验包只对字节做验签，不绑定具体编码。
 */
export interface SenderCertificate {
  sender: string;
  /** 证书绑定的身份公钥（会话长期身份密钥）。 */
  identityKey: Uint8Array;
  /** 有效期起点，epoch 毫秒。 */
  validFrom: number;
  /** 有效期终点，epoch 毫秒。 */
  validUntil: number;
  /** 被签名的规范化证书负载。 */
  signedData: Uint8Array;
  /** 签发者对 signedData 的签名。 */
  signature: Uint8Array;
  /** 签发该证书的根标识，用于在受信根集合中选择验证密钥。 */
  rootId: string;
}

/** 受信根证书/根公钥配置。 */
export interface TrustedRoot {
  rootId: string;
  publicKey: Uint8Array;
  verifier?: SignatureVerifier;
}

export interface VerifySenderCertificateOptions {
  certificate: SenderCertificate;
  /** 本地会话使用的身份公钥，必须与证书绑定密钥一致。 */
  sessionIdentityKey: Uint8Array;
  /** 消息内层声明的发送者，必须与证书 sender 一致。 */
  declaredSender: string;
  trustedRoots: readonly TrustedRoot[];
  /** 注入当前时间（epoch 毫秒），默认 Date.now()。 */
  now?: number;
  /** 时钟容差，默认 {@link CERTIFICATE_CLOCK_SKEW_MS}（5 分钟）。 */
  clockSkewMs?: number;
  /** 可选的发送者归一化（小写、去空格等），对两边同时生效。 */
  normalizeSender?: (sender: string) => string;
  /** 未在根上单独指定验证器时使用的默认验证器。 */
  defaultVerifier?: SignatureVerifier;
}

function assertBytes(value: unknown, field: string): asserts value is Uint8Array {
  if (!(value instanceof Uint8Array) || value.length === 0) {
    throw new CertificateError("CERT_MALFORMED", `certificate field ${field} is invalid`);
  }
}

function validateStructure(certificate: SenderCertificate): void {
  if (typeof certificate.sender !== "string" || certificate.sender.length === 0) {
    throw new CertificateError("CERT_MALFORMED", "certificate sender is invalid");
  }
  if (
    typeof certificate.rootId !== "string" ||
    certificate.rootId.length === 0
  ) {
    throw new CertificateError("CERT_MALFORMED", "certificate rootId is invalid");
  }
  assertBytes(certificate.identityKey, "identityKey");
  assertBytes(certificate.signedData, "signedData");
  assertBytes(certificate.signature, "signature");
  if (
    !Number.isFinite(certificate.validFrom) ||
    !Number.isFinite(certificate.validUntil)
  ) {
    throw new CertificateError("CERT_MALFORMED", "certificate validity period is invalid");
  }
}

/**
 * 客户端侧校验密封发送者证书，依次验证：
 *
 * 1. 证书结构完整；
 * 2. 证书签名链至受信根（防止伪造发送者证书）；
 * 3. 证书在有效期内，允许 ±5 分钟时钟容差；过期/未生效抛
 *    {@link CertificateExpiredError}，调用方据此静默丢弃、不发回执；
 * 4. 证书身份密钥与本地会话身份密钥常数时间相等；
 * 5. 证书发送者与消息内声明发送者相等（可选归一化）。
 */
export async function verifySenderCertificate(
  options: VerifySenderCertificateOptions,
): Promise<void> {
  const {
    certificate,
    sessionIdentityKey,
    declaredSender,
    trustedRoots,
  } = options;
  const now = options.now ?? Date.now();
  const clockSkewMs =
    options.clockSkewMs ?? CERTIFICATE_CLOCK_SKEW_MS;
  const fallbackVerifier = options.defaultVerifier ?? defaultSignatureVerifier;

  validateStructure(certificate);

  const root = trustedRoots.find(
    (candidate) => candidate.rootId === certificate.rootId,
  );
  if (!root) {
    throw new CertificateError(
      "CERT_UNTRUSTED_ROOT",
      "certificate was not issued by a trusted root",
    );
  }

  const verifier = root.verifier ?? fallbackVerifier;
  const signatureValid = await verifier.verify(
    root.publicKey,
    certificate.signature,
    certificate.signedData,
  );
  if (!signatureValid) {
    throw new CertificateError(
      "CERT_UNTRUSTED_ROOT",
      "certificate signature failed verification",
    );
  }

  if (
    now < certificate.validFrom - clockSkewMs ||
    now > certificate.validUntil + clockSkewMs
  ) {
    throw new CertificateExpiredError();
  }

  if (!timingSafeEqualBytes(certificate.identityKey, sessionIdentityKey)) {
    throw new CertificateError(
      "CERT_IDENTITY_MISMATCH",
      "certificate identity key does not match the session identity key",
    );
  }

  const normalize = options.normalizeSender ?? ((value: string) => value);
  if (
    !timingSafeEqualString(
      normalize(certificate.sender),
      normalize(declaredSender),
    )
  ) {
    throw new CertificateError(
      "CERT_SENDER_MISMATCH",
      "certificate sender does not match the sender declared in the message",
    );
  }
}
