import {
  createHash,
  timingSafeEqual,
  type TimingSafeEqualBinary,
} from "node:crypto";

/**
 * GNAP 交互结束（interaction finish）回调校验组件。
 *
 * 防御场景：攻击者拿到受害者浏览器里的回调请求后，把回调参数中的
 * `interact_ref` 换成自己的引用，试图让客户端把 AS 的授权结果绑定到
 * 攻击者控制的交互上。校验依据见 GNAP Core (RFC 9724) 交互哈希计算：
 *
 *   base64url( SHA-256( client_nonce + LF + as_nonce + LF +
 *                       interact_ref + LF + 授权请求URL ) )
 *
 * 只有满足以下全部条件时，客户端才允许使用回调里的 `interact_ref`
 * 向 AS 续期（轮询令牌/事务）：
 *   1. 重算的哈希与回调 `hash` 参数常量时间相等；
 *   2. 回调发生在该事务登记时绑定的浏览器会话里；
 *   3. 该事务此前从未成功处理过回调（每事务仅一次）。
 */

const LF = "\n";

/** 单个 GNAP 事务在客户端侧需要持久保存的交互上下文。 */
export interface GnapTransaction {
  /** 客户端生成、随授权请求发给 AS 的随机 nonce。 */
  clientNonce: string;
  /** AS 在授权响应中返回、用于交互校验的 nonce。 */
  asNonce: string;
  /**
   * 登记该交互的浏览器会话标识（如已认证的会话 ID）。
   * 回调必须携带同一标识，防止跨会话投递。
   */
  browserSessionId: string;
  /** 发起授权请求时的完整 URL（哈希的最后一段输入）。 */
  authorizationRequestUrl: string;
  /** 已成功处理一次回调后保存的 interact_ref。 */
  interactRef?: string | undefined;
  /** 该事务是否已经消费过一次回调。 */
  callbackReceived: boolean;
}

/** 登记新事务时由调用方提供的参数。 */
export interface RegisterTransactionInput {
  clientNonce: string;
  asNonce: string;
  browserSessionId: string;
  authorizationRequestUrl: string;
}

/** 浏览器回调（重定向）中与交互结束相关的参数。 */
export interface InteractionCallback {
  /** 回调 URL 查询参数里的 interact_ref（可能被攻击者篡改）。 */
  interactRef: string;
  /** 回调 URL 查询参数里的 hash，由 AS 计算。 */
  hash: string;
  /**
   * 承载本次回调的浏览器会话标识，由调用方从已认证的会话/Cookie
   * 中解析后传入；组件不信任回调 URL 自身携带的会话声明。
   */
  browserSessionId: string;
}

/** 校验通过后的结果。 */
export interface VerifiedInteraction {
  transactionId: string;
  /** 仅在哈希与会话均校验通过后才应被用于续期的 interact_ref。 */
  interactRef: string;
}

/** 校验失败的错误码，供调用方按类别记日志/审计。 */
export type GnapVerificationErrorCode =
  | "UNKNOWN_TRANSACTION"
  | "DUPLICATE_CALLBACK"
  | "CROSS_SESSION_CALLBACK"
  | "INVALID_HASH"
  | "INVALID_INPUT";

export class GnapVerificationError extends Error {
  readonly code: GnapVerificationErrorCode;

  constructor(code: GnapVerificationErrorCode, message: string) {
    super(message);
    this.name = "GnapVerificationError";
    this.code = code;
  }
}

function requireNonEmpty(value: string, field: string): void {
  if (typeof value !== "string" || value.length === 0) {
    throw new GnapVerificationError(
      "INVALID_INPUT",
      `字段 ${field} 必须是非空字符串`,
    );
  }
}

/**
 * 计算 GNAP 交互哈希。
 * 各字段以单个 LF (0x0A) 连接，UTF-8 编码后做 SHA-256，输出无填充
 * base64url 字符串。
 */
export function calculateInteractionHash(
  clientNonce: string,
  asNonce: string,
  interactRef: string,
  authorizationRequestUrl: string,
): string {
  const payload = [
    clientNonce,
    asNonce,
    interactRef,
    authorizationRequestUrl,
  ].join(LF);
  return createHash("sha256").update(payload, "utf8").digest("base64url");
}

/**
 * 去掉 base64url 末尾可选的 "=" 填充，保证与无填充摘要按相同编码比较。
 * 不做大小写归一：base64url 字母表本身大小写敏感。
 */
function normalizeBase64url(value: string): string {
  return value.endsWith("=") ? value.replace(/=+$/, "") : value;
}

/**
 * 两个 base64url 字符串的常量时间比较。
 * 长度不同时立即返回 false（长度本就不是秘密，且 timingSafeEqual
 * 要求等长 Buffer）；长度相同时走恒定时间比较，避免逐字符提前退出
 * 泄露哈希前缀。
 */
export function constantTimeHashEquals(a: string, b: string): boolean {
  const left = normalizeBase64url(a);
  const right = normalizeBase64url(b);
  if (left.length !== right.length) {
    return false;
  }
  const leftBuffer = Buffer.from(left, "utf8");
  const rightBuffer = Buffer.from(right, "utf8");
  return timingSafeEqual(
    leftBuffer as TimingSafeEqualBinary,
    rightBuffer as TimingSafeEqualBinary,
  );
}

/**
 * 事务型校验器：负责登记事务、处理交互结束回调。
 *
 * 实例内部用 Map 保存事务状态；每次成功回调都会把事务标记为
 * `callbackReceived`，重复回调一律拒绝。失败的校验不会消费事务，
 * 避免攻击者用伪造回调制造拒绝服务。
 */
export class GnapInteractionVerifier {
  readonly #transactions = new Map<string, GnapTransaction>();

  /**
   * 登记一个等待浏览器交互的 GNAP 事务。
   * 返回的事务 ID 由调用方自行关联（也可直接用 AS 的 transaction_handle）。
   */
  register(
    transactionId: string,
    input: RegisterTransactionInput,
  ): GnapTransaction {
    requireNonEmpty(transactionId, "transactionId");
    requireNonEmpty(input.clientNonce, "clientNonce");
    requireNonEmpty(input.asNonce, "asNonce");
    requireNonEmpty(input.browserSessionId, "browserSessionId");
    requireNonEmpty(input.authorizationRequestUrl, "authorizationRequestUrl");

    const transaction: GnapTransaction = {
      clientNonce: input.clientNonce,
      asNonce: input.asNonce,
      browserSessionId: input.browserSessionId,
      authorizationRequestUrl: input.authorizationRequestUrl,
      callbackReceived: false,
    };
    this.#transactions.set(transactionId, transaction);
    return transaction;
  }

  /** 取出事务快照，找不到时抛 UNKNOWN_TRANSACTION。 */
  getTransaction(transactionId: string): GnapTransaction {
    const transaction = this.#transactions.get(transactionId);
    if (transaction === undefined) {
      throw new GnapVerificationError(
        "UNKNOWN_TRANSACTION",
        `未知事务 ${transactionId}`,
      );
    }
    return { ...transaction };
  }

  /**
   * 处理一次交互结束回调。
   *
   * 校验顺序（全部通过才落状态、才返回可续期的 interact_ref）：
   *   未知事务 -> 重复回调 -> 跨会话 -> 哈希不匹配。
   * 任何一步失败都抛 {@link GnapVerificationError}，且不修改事务状态。
   */
  verifyCallback(
    transactionId: string,
    callback: InteractionCallback,
  ): VerifiedInteraction {
    requireNonEmpty(transactionId, "transactionId");
    requireNonEmpty(callback.interactRef, "interactRef");
    requireNonEmpty(callback.hash, "hash");
    requireNonEmpty(callback.browserSessionId, "browserSessionId");

    const transaction = this.#transactions.get(transactionId);
    if (transaction === undefined) {
      throw new GnapVerificationError(
        "UNKNOWN_TRANSACTION",
        `未知事务 ${transactionId}`,
      );
    }

    if (transaction.callbackReceived) {
      throw new GnapVerificationError(
        "DUPLICATE_CALLBACK",
        `事务 ${transactionId} 已处理过一次交互回调`,
      );
    }

    if (callback.browserSessionId !== transaction.browserSessionId) {
      throw new GnapVerificationError(
        "CROSS_SESSION_CALLBACK",
        "回调未发生在登记该事务的浏览器会话中",
      );
    }

    const expectedHash = calculateInteractionHash(
      transaction.clientNonce,
      transaction.asNonce,
      callback.interactRef,
      transaction.authorizationRequestUrl,
    );

    if (!constantTimeHashEquals(expectedHash, callback.hash)) {
      throw new GnapVerificationError(
        "INVALID_HASH",
        "交互哈希校验失败：interact_ref 可能被伪造或替换",
      );
    }

    transaction.callbackReceived = true;
    transaction.interactRef = callback.interactRef;

    return {
      transactionId,
      interactRef: callback.interactRef,
    };
  }

  /**
   * 仅当回调已成功校验后，返回可用于向 AS 续期的 interact_ref；
   * 未完成校验时返回 undefined，调用方不得用回调里的原始值续期。
   */
  getContinuationInteractRef(transactionId: string): string | undefined {
    const transaction = this.#transactions.get(transactionId);
    if (transaction === undefined || !transaction.callbackReceived) {
      return undefined;
    }
    return transaction.interactRef;
  }

  /** 删除事务（续期完成或超时清理时调用）。 */
  remove(transactionId: string): void {
    this.#transactions.delete(transactionId);
  }
}
