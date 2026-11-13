import { normalizeAddress } from "./bytes.js";
import { type EIP712Domain } from "./domain.js";
import { SignatureVerificationError, VerificationErrorCode } from "./errors.js";
import { type InMemoryNonceStore, type NonceStore } from "./nonce.js";
import { verifyTypedData } from "./signature.js";
import type {
  Keccak256Fn,
  RecoverAddressFn,
  TypedDataTypes,
  TypedDataValue,
} from "./types.js";

export interface RelayerConfig {
  /** 本中继服务绑定的链 id，域中 chainId 必须与之相等。 */
  chainId: bigint;
  /** 目标合约地址，域中 verifyingContract 必须与之相等。 */
  targetContract: string;
  keccak256: Keccak256Fn;
  recoverAddress: RecoverAddressFn;
  /** nonce 存储，默认使用进程内内存实现。 */
  nonceStore?: NonceStore;
  /** 消息中 nonce 字段名，默认 nonce。 */
  nonceField?: string;
  /** 消息中 deadline 字段名，默认 deadline。 */
  deadlineField?: string;
  /** 消息中签名者字段名，默认 signer。 */
  signerField?: string;
  /** 当前时间（Unix 秒），默认 Date.now，测试可注入。 */
  now?: () => bigint;
}

export interface RelayRequest {
  domain: EIP712Domain;
  types: TypedDataTypes;
  primaryType: string;
  message: Record<string, TypedDataValue>;
  signature: string | Uint8Array;
}

export interface RelayResult {
  signer: string;
  /** 本次消费掉的 nonce。 */
  nonce: bigint;
}

/**
 * 成功提交后触发的回调（例如广播链上交易）。在 nonce 已原子递增之后调用；
 * 回调抛错不会回滚已消费的 nonce，与链上先记账后执行的语义一致。
 */
export type RelayAcceptedCallback = (
  request: RelayRequest,
  result: RelayResult,
) => void | Promise<void>;

function toBigInt(value: TypedDataValue, field: string): bigint {
  if (typeof value === "bigint") return value;
  if (typeof value === "number" && Number.isSafeInteger(value)) {
    return BigInt(value);
  }
  if (typeof value === "string" && /^\d+$/.test(value)) {
    return BigInt(value);
  }
  throw new SignatureVerificationError(
    VerificationErrorCode.INVALID_NONCE,
    `message field ${field} must be an unsigned integer`,
  );
}

/**
 * 类型化数据中继：强制域绑定（chainId / verifyingContract，缺失即拒绝）、
 * nonce 必须等于链上当前值且提交成功后递增、deadline 过期拒绝、
 * 恢复地址必须等于消息声明的签名者，并拒绝高 s 与非法 v 的签名。
 */
export class TypedDataRelayer {
  private readonly chainId: bigint;
  private readonly targetContract: string;
  private readonly keccak256: Keccak256Fn;
  private readonly recoverAddress: RecoverAddressFn;
  private readonly nonceStore: NonceStore;
  private readonly nonceField: string;
  private readonly deadlineField: string;
  private readonly signerField: string;
  private readonly now: () => bigint;

  constructor(config: RelayerConfig) {
    this.chainId = BigInt(config.chainId);
    this.targetContract = normalizeAddress(config.targetContract);
    this.keccak256 = config.keccak256;
    this.recoverAddress = config.recoverAddress;
    this.nonceStore = config.nonceStore ?? new InMemoryNonceStore();
    this.nonceField = config.nonceField ?? "nonce";
    this.deadlineField = config.deadlineField ?? "deadline";
    this.signerField = config.signerField ?? "signer";
    this.now = config.now ?? (() => BigInt(Math.floor(Date.now() / 1000)));
  }

  /** 查询签名者当前链上 nonce。 */
  getNonce(signer: string): Promise<bigint> {
    return this.nonceStore.get(normalizeAddress(signer));
  }

  private assertDomain(domain: EIP712Domain): void {
    if (domain.chainId === undefined) {
      throw new SignatureVerificationError(
        VerificationErrorCode.MISSING_CHAIN_ID,
        "domain must declare chainId",
      );
    }
    if (BigInt(domain.chainId) !== this.chainId) {
      throw new SignatureVerificationError(
        VerificationErrorCode.CHAIN_ID_MISMATCH,
        `domain chainId ${domain.chainId.toString()} does not match configured chain ${this.chainId.toString()}`,
      );
    }

    if (domain.verifyingContract === undefined) {
      throw new SignatureVerificationError(
        VerificationErrorCode.MISSING_VERIFYING_CONTRACT,
        "domain must declare verifyingContract",
      );
    }
    let domainContract: string;
    try {
      domainContract = normalizeAddress(domain.verifyingContract);
    } catch {
      throw new SignatureVerificationError(
        VerificationErrorCode.VERIFYING_CONTRACT_MISMATCH,
        "domain verifyingContract is not a valid address",
      );
    }
    if (domainContract !== this.targetContract) {
      throw new SignatureVerificationError(
        VerificationErrorCode.VERIFYING_CONTRACT_MISMATCH,
        `domain verifyingContract ${domainContract} does not match target contract ${this.targetContract}`,
      );
    }
  }

  private readSigner(message: Record<string, TypedDataValue>): string {
    const raw = message[this.signerField];
    if (raw === undefined || raw === null) {
      throw new SignatureVerificationError(
        VerificationErrorCode.MISSING_SIGNER,
        `message must declare signer in field ${this.signerField}`,
      );
    }
    if (typeof raw !== "string") {
      throw new SignatureVerificationError(
        VerificationErrorCode.MISSING_SIGNER,
        `signer field ${this.signerField} must be an address string`,
      );
    }
    try {
      return normalizeAddress(raw);
    } catch {
      throw new SignatureVerificationError(
        VerificationErrorCode.MISSING_SIGNER,
        `signer field ${this.signerField} is not a valid address`,
      );
    }
  }

  private readNonce(message: Record<string, TypedDataValue>): bigint {
    const raw = message[this.nonceField];
    if (raw === undefined || raw === null) {
      throw new SignatureVerificationError(
        VerificationErrorCode.MISSING_NONCE,
        `message must declare nonce in field ${this.nonceField}`,
      );
    }
    return toBigInt(raw, this.nonceField);
  }

  private assertDeadline(message: Record<string, TypedDataValue>): void {
    const raw = message[this.deadlineField];
    if (raw === undefined || raw === null) {
      throw new SignatureVerificationError(
        VerificationErrorCode.MISSING_DEADLINE,
        `message must declare deadline in field ${this.deadlineField}`,
      );
    }
    const deadline = toBigInt(raw, this.deadlineField);
    const currentTime = this.now();
    if (deadline < currentTime) {
      throw new SignatureVerificationError(
        VerificationErrorCode.DEADLINE_EXPIRED,
        `deadline ${deadline.toString()} is before current time ${currentTime.toString()}`,
      );
    }
  }

  /**
   * 校验并提交一次中继请求。
   * 顺序：域绑定 → 签名者/nonce/deadline 字段 → 签名与恢复地址校验
   * → 原子比对并递增 nonce → 成功回调。
   */
  async relay(
    request: RelayRequest,
    onAccepted?: RelayAcceptedCallback,
  ): Promise<RelayResult> {
    this.assertDomain(request.domain);
    const signer = this.readSigner(request.message);
    const nonce = this.readNonce(request.message);
    this.assertDeadline(request.message);

    // 内部完成 v/s malleability 检查与恢复地址比对。
    await verifyTypedData(
      { keccak256: this.keccak256, recoverAddress: this.recoverAddress },
      {
        domain: request.domain,
        types: request.types,
        primaryType: request.primaryType,
        message: request.message,
        expectedSigner: signer,
        signature: request.signature,
      },
    );

    const consumed = await this.nonceStore.checkAndIncrement(signer, nonce);
    if (!consumed) {
      const current = await this.nonceStore.get(signer);
      throw new SignatureVerificationError(
        VerificationErrorCode.NONCE_MISMATCH,
        `nonce ${nonce.toString()} does not match on-chain current value ${current.toString()}`,
      );
    }

    const result: RelayResult = { signer, nonce };
    if (onAccepted) {
      await onAccepted(request, result);
    }
    return result;
  }
}
