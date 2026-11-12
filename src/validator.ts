import { decodeData, encodeData } from './codec.ts';
import { ValidationError, ValidationErrorCode } from './errors.ts';
import {
  BodyKind,
  MESSAGE_BODY_KIND,
  MessageType,
  type Message,
  type MessageData,
} from './types.ts';

/** 允许的最大未来时间戳偏移（10 分钟）。 */
export const MAX_FUTURE_TIMESTAMP_MS = 10 * 60 * 1000;

export interface VerifySignatureInput {
  hash: Uint8Array;
  signature: Uint8Array;
  signer: Uint8Array;
  signatureScheme: Message['signatureScheme'];
}

/** 由调用方注入的密码学原语与环境依赖。 */
export interface ValidatorDeps {
  /** 对 data_bytes 计算哈希，结果须与 message.hash 比较。 */
  hash: (dataBytes: Uint8Array, hashScheme: Message['hashScheme']) => Uint8Array;
  /** 验证 signer 对 message.hash 的签名。 */
  verifySignature: (input: VerifySignatureInput) => boolean;
  /** 解析 fid 当前的托管密钥（签名者管理消息必须由它签发）。 */
  resolveCustodySigner: (fid: number) => Uint8Array | undefined;
  /** 可选：自定义 data_bytes 编解码，默认使用规范 JSON。 */
  encode?: (data: MessageData) => Uint8Array;
  decode?: (dataBytes: Uint8Array) => MessageData;
  /** 可选：注入时钟（毫秒），默认 Date.now。 */
  clock?: () => number;
}

export interface AcceptResult {
  accepted: true;
  message: Message;
  /** 因本次 SignerRemove 被逐出的历史消息哈希。 */
  evictedHashes: Uint8Array[];
}

interface SignerState {
  key: Uint8Array;
  addedAt: number;
  removedAt: number | null;
}

export function bytesToHex(bytes: Uint8Array): string {
  let hex = '';
  for (const byte of bytes) {
    hex += byte.toString(16).padStart(2, '0');
  }
  return hex;
}

function bytesEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.length !== right.length) {
    return false;
  }
  for (let index = 0; index < left.length; index += 1) {
    if (left[index] !== right[index]) {
      return false;
    }
  }
  return true;
}

function deepEqual(left: unknown, right: unknown): boolean {
  if (left === right) {
    return true;
  }
  if (left instanceof Uint8Array && right instanceof Uint8Array) {
    return bytesEqual(left, right);
  }
  if (Array.isArray(left) && Array.isArray(right)) {
    return (
      left.length === right.length &&
      left.every((entry, index) => deepEqual(entry, right[index]))
    );
  }
  if (
    typeof left === 'object' &&
    typeof right === 'object' &&
    left !== null &&
    right !== null &&
    Object.getPrototypeOf(left) === Object.prototype &&
    Object.getPrototypeOf(right) === Object.prototype
  ) {
    const leftRecord = left as Record<string, unknown>;
    const rightRecord = right as Record<string, unknown>;
    const leftKeys = Object.keys(leftRecord);
    const rightKeys = Object.keys(rightRecord);
    return (
      leftKeys.length === rightKeys.length &&
      leftKeys.every(
        (key) =>
          Object.prototype.hasOwnProperty.call(rightRecord, key) &&
          deepEqual(leftRecord[key], rightRecord[key]),
      )
    );
  }
  return false;
}

function isFiniteNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

/**
 * 有状态的消息校验器：除无状态的格式/哈希/签名校验外，
 * 还维护每个 fid 的活跃签名者集合与已入库消息，
 * 处理 SignerRemove 逐出与同时间戳平局规则。
 */
export class MessageValidator {
  private readonly deps: Required<Omit<ValidatorDeps, 'encode' | 'decode'>> & {
    encode: (data: MessageData) => Uint8Array;
    decode: (dataBytes: Uint8Array) => MessageData;
  };

  private readonly signerStates = new Map<number, Map<string, SignerState>>();
  private readonly storedMessages = new Map<number, Map<string, Message>>();

  constructor(deps: ValidatorDeps) {
    this.deps = {
      ...deps,
      encode: deps.encode ?? encodeData,
      decode: deps.decode ?? decodeData,
      clock: deps.clock ?? (() => Date.now()),
    };
  }

  /**
   * 校验并入库一条消息。校验失败抛出 ValidationError；
   * 成功返回 AcceptResult，SignerRemove 时携带被逐出的消息哈希。
   */
  validate(message: Message): AcceptResult {
    const data = this.checkStructure(message);
    this.checkTypeBody(data);
    this.checkHash(message);
    this.checkDataBytes(message);
    this.checkTimestamp(data);
    this.checkSignature(message);

    const fidStates = this.signerStatesFor(data.fid);
    const fidStore = this.storedMessagesFor(data.fid);
    const hashKey = bytesToHex(message.hash);
    if (fidStore.has(hashKey)) {
      throw new ValidationError(
        ValidationErrorCode.DUPLICATE_MESSAGE,
        '消息已入库，拒绝重复消息',
      );
    }

    const evictedHashes =
      data.type === MessageType.SIGNER_ADD
        ? this.applySignerAdd(message, fidStates)
        : data.type === MessageType.SIGNER_REMOVE
          ? this.applySignerRemove(message, fidStates, fidStore)
          : this.applyAppMessage(message, fidStates);

    fidStore.set(hashKey, message);
    return { accepted: true, message, evictedHashes };
  }

  isActive(fid: number, signer: Uint8Array): boolean {
    const state = this.signerStatesFor(fid).get(bytesToHex(signer));
    return state !== undefined && state.removedAt === null;
  }

  getActiveSigners(fid: number): Uint8Array[] {
    return [...this.signerStatesFor(fid).values()]
      .filter((state) => state.removedAt === null)
      .map((state) => state.key);
  }

  getStoredMessages(fid: number): Message[] {
    return [...this.storedMessagesFor(fid).values()];
  }

  private checkStructure(message: Message): MessageData {
    if (message === null || typeof message !== 'object') {
      throw this.malformed('消息必须是对象');
    }
    const { data, dataBytes, hash, signature, signer } = message;
    if (
      !(dataBytes instanceof Uint8Array) ||
      !(hash instanceof Uint8Array) ||
      !(signature instanceof Uint8Array) ||
      !(signer instanceof Uint8Array)
    ) {
      throw this.malformed('dataBytes/hash/signature/signer 必须是字节数组');
    }
    if (data === null || typeof data !== 'object') {
      throw this.malformed('data 必须是对象');
    }
    if (
      !isFiniteNonNegativeInteger(data.fid) ||
      !isFiniteNonNegativeInteger(data.timestamp) ||
      !isFiniteNonNegativeInteger(data.network)
    ) {
      throw this.malformed('fid/timestamp/network 必须是非负整数');
    }
    if (data.body === null || typeof data.body !== 'object') {
      throw this.malformed('body 必须是对象');
    }
    if (!Object.values(MessageType).includes(data.type)) {
      throw new ValidationError(
        ValidationErrorCode.MALFORMED_MESSAGE,
        `未知消息类型: ${String(data.type)}`,
      );
    }
    return data;
  }

  private checkTypeBody(data: MessageData): void {
    const expectedKind = MESSAGE_BODY_KIND[data.type];
    if (data.body.bodyType !== expectedKind) {
      throw new ValidationError(
        ValidationErrorCode.TYPE_BODY_MISMATCH,
        `消息类型 ${data.type} 要求正文 ${expectedKind}，实际为 ${String(data.body.bodyType)}`,
      );
    }
  }

  private checkHash(message: Message): void {
    const expected = this.deps.hash(message.dataBytes, message.hashScheme);
    if (!bytesEqual(expected, message.hash)) {
      throw new ValidationError(
        ValidationErrorCode.HASH_MISMATCH,
        'hash 与对 data_bytes 计算的结果不一致',
      );
    }
  }

  private checkDataBytes(message: Message): void {
    let decoded: MessageData;
    try {
      decoded = this.deps.decode(message.dataBytes);
    } catch {
      throw new ValidationError(
        ValidationErrorCode.DATA_BYTES_MISMATCH,
        'data_bytes 无法重新解码',
      );
    }
    if (!deepEqual(decoded, message.data)) {
      throw new ValidationError(
        ValidationErrorCode.DATA_BYTES_MISMATCH,
        'data_bytes 重新解码后的字段与结构化 data 不一致',
      );
    }
    const reencoded = this.deps.encode(message.data);
    if (!bytesEqual(reencoded, message.dataBytes)) {
      throw new ValidationError(
        ValidationErrorCode.DATA_BYTES_MISMATCH,
        '结构化 data 重新编码后的字节与 data_bytes 不一致',
      );
    }
  }

  private checkTimestamp(data: MessageData): void {
    const now = this.deps.clock();
    if (data.timestamp > now + MAX_FUTURE_TIMESTAMP_MS) {
      throw new ValidationError(
        ValidationErrorCode.FUTURE_TIMESTAMP,
        `时间戳 ${data.timestamp} 超出当前时间允许的 10 分钟未来窗口`,
      );
    }
  }

  private checkSignature(message: Message): void {
    let valid: boolean;
    try {
      valid = this.deps.verifySignature({
        hash: message.hash,
        signature: message.signature,
        signer: message.signer,
        signatureScheme: message.signatureScheme,
      });
    } catch {
      valid = false;
    }
    if (!valid) {
      throw new ValidationError(
        ValidationErrorCode.INVALID_SIGNATURE,
        '签名验证失败',
      );
    }
  }

  private applyAppMessage(
    message: Message,
    fidStates: Map<string, SignerState>,
  ): Uint8Array[] {
    const state = fidStates.get(bytesToHex(message.signer));
    if (state === undefined || state.removedAt !== null) {
      throw new ValidationError(
        ValidationErrorCode.SIGNER_NOT_ACTIVE,
        '签名者不在该 fid 的当前活跃签名者集合中',
      );
    }
    return [];
  }

  private applySignerAdd(
    message: Message,
    fidStates: Map<string, SignerState>,
  ): Uint8Array[] {
    this.checkCustodySignature(message);
    const { signer: addedSigner } = message.data.body as Extract<
      MessageData['body'],
      { bodyType: BodyKind.SIGNER_ADD }
    >;
    const key = bytesToHex(addedSigner);
    const state = fidStates.get(key);
    if (state === undefined) {
      fidStates.set(key, {
        key: addedSigner,
        addedAt: message.data.timestamp,
        removedAt: null,
      });
      return [];
    }
    // 已活跃：幂等接受。已被移除且移除时间不早于本次添加时，
    // 同时间戳平局由移除胜出，更旧的添加也不能复活签名者。
    if (state.removedAt !== null && state.removedAt >= message.data.timestamp) {
      throw new ValidationError(
        ValidationErrorCode.SIGNER_ADD_SUPPRESSED,
        'SignerAdd 与 SignerRemove 时间戳相同或更旧，移除胜出',
      );
    }
    if (state.removedAt !== null) {
      state.removedAt = null;
      state.addedAt = message.data.timestamp;
    }
    return [];
  }

  private applySignerRemove(
    message: Message,
    fidStates: Map<string, SignerState>,
    fidStore: Map<string, Message>,
  ): Uint8Array[] {
    this.checkCustodySignature(message);
    const { signer: removedSigner } = message.data.body as Extract<
      MessageData['body'],
      { bodyType: BodyKind.SIGNER_REMOVE }
    >;
    const key = bytesToHex(removedSigner);
    const state = fidStates.get(key);
    if (state === undefined || state.addedAt > message.data.timestamp) {
      throw new ValidationError(
        ValidationErrorCode.SIGNER_NOT_ACTIVE,
        '被移除的签名者从未在该 fid 下生效',
      );
    }
    if (state.removedAt !== null && state.removedAt <= message.data.timestamp) {
      // 已被更新或同等时间戳的移除处理过：幂等接受，不再重复逐出。
      return [];
    }
    state.removedAt = message.data.timestamp;

    const evictedHashes: Uint8Array[] = [];
    for (const [storedKey, stored] of fidStore) {
      if (
        stored.data.type !== MessageType.SIGNER_ADD &&
        stored.data.type !== MessageType.SIGNER_REMOVE &&
        bytesEqual(stored.signer, removedSigner)
      ) {
        fidStore.delete(storedKey);
        evictedHashes.push(stored.hash);
      }
    }
    return evictedHashes;
  }

  private checkCustodySignature(message: Message): void {
    const custody = this.deps.resolveCustodySigner(message.data.fid);
    if (custody === undefined || !bytesEqual(custody, message.signer)) {
      throw new ValidationError(
        ValidationErrorCode.UNKNOWN_CUSTODY,
        '签名者管理消息必须由该 fid 的托管密钥签发',
      );
    }
  }

  private signerStatesFor(fid: number): Map<string, SignerState> {
    let states = this.signerStates.get(fid);
    if (states === undefined) {
      states = new Map();
      this.signerStates.set(fid, states);
    }
    return states;
  }

  private storedMessagesFor(fid: number): Map<string, Message> {
    let store = this.storedMessages.get(fid);
    if (store === undefined) {
      store = new Map();
      this.storedMessages.set(fid, store);
    }
    return store;
  }

  private malformed(reason: string): ValidationError {
    return new ValidationError(ValidationErrorCode.MALFORMED_MESSAGE, reason);
  }
}
