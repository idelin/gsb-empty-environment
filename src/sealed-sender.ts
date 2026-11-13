import {
  verifyUnidentifiedAccessKey,
  type AccessKeyRegistry,
} from "./access-key.ts";
import {
  verifySenderCertificate,
  type SenderCertificate,
  type TrustedRoot,
} from "./certificate.ts";
import type { SignatureVerifier } from "./crypto.ts";
import {
  AccessKeyError,
  CertificateError,
  isCertificateExpiredError,
} from "./errors.ts";

/** 一条待处理的密封发送者消息。 */
export interface SealedSenderMessage {
  /** 请求头中的 Unidentified-Access-Key（Uint8Array/hex/base64）。 */
  unidentifiedAccessKey: unknown;
  recipientId: string;
  /** 消息携带的发送者证书。 */
  senderCertificate: SenderCertificate;
  /** 内层消息声明的发送者标识。 */
  declaredSender: string;
  /** 本地解密后得到的会话长期身份公钥。 */
  sessionIdentityKey: Uint8Array;
}

export type SealedSenderRejectionReason =
  | "ACCESS_KEY_REJECTED"
  | "CERT_MALFORMED"
  | "CERT_UNTRUSTED_ROOT"
  | "CERT_EXPIRED"
  | "CERT_IDENTITY_MISMATCH"
  | "CERT_SENDER_MISMATCH";

export interface SealedSenderRejection {
  accepted: false;
  reason: SealedSenderRejectionReason;
  error: AccessKeyError | CertificateError;
  /**
   * 是否必须静默丢弃。
   * 仅证书过期/未生效为 true：消息直接丢弃，不发送任何回执
   * （投递回执、已读回执、错误回执均不发）。
   */
  silent: boolean;
}

export interface SealedSenderAcceptance {
  accepted: true;
  recipientId: string;
  sender: string;
  /** 校验通过后才允许投递，并可正常发送投递回执。 */
  deliveryReceiptAllowed: true;
}

export type SealedSenderResult =
  | SealedSenderAcceptance
  | SealedSenderRejection;

export interface ProcessSealedSenderOptions {
  message: SealedSenderMessage;
  registry: AccessKeyRegistry;
  trustedRoots: readonly TrustedRoot[];
  now?: number;
  clockSkewMs?: number;
  normalizeSender?: (sender: string) => string;
  defaultVerifier?: SignatureVerifier;
}

function reject(
  error: AccessKeyError | CertificateError,
): SealedSenderRejection {
  return {
    accepted: false,
    reason: error.code,
    error,
    silent: isCertificateExpiredError(error),
  };
}

/**
 * 密封发送者消息的完整准入流水线：
 *
 * 1. 服务端常数时间校验 Unidentified-Access-Key，失败统一拒绝；
 * 2. 客户端侧校验发送者证书（受信根、有效期 5 分钟容差、
 *    身份密钥绑定、发送者绑定）；
 * 3. 全部通过才返回 accepted，允许投递并发送回执；
 *    证书过期返回 silent 拒绝，仅丢弃、不发回执。
 *
 * 该函数不抛校验类异常，而是返回可判别联合，方便路由层统一处理；
 * 注册表自身的基础设施故障仍会向上抛出。
 */
export async function processSealedSender(
  options: ProcessSealedSenderOptions,
): Promise<SealedSenderResult> {
  const { message } = options;

  try {
    const record = await verifyUnidentifiedAccessKey({
      presentedKey: message.unidentifiedAccessKey,
      recipientId: message.recipientId,
      registry: options.registry,
    });

    await verifySenderCertificate({
      certificate: message.senderCertificate,
      sessionIdentityKey: message.sessionIdentityKey,
      declaredSender: message.declaredSender,
      trustedRoots: options.trustedRoots,
      now: options.now,
      clockSkewMs: options.clockSkewMs,
      normalizeSender: options.normalizeSender,
      defaultVerifier: options.defaultVerifier,
    });

    return {
      accepted: true,
      recipientId: record.recipientId,
      sender: message.senderCertificate.sender,
      deliveryReceiptAllowed: true,
    };
  } catch (error) {
    if (error instanceof AccessKeyError || error instanceof CertificateError) {
      return reject(error);
    }
    throw error;
  }
}

/** 便捷守卫：该结果是否为“过期证书、静默丢弃、不发回执”。 */
export function isSilentDrop(result: SealedSenderResult): boolean {
  return !result.accepted && result.silent;
}
