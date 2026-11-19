import {
  MessageType,
  TYPE_TO_BODY_KIND,
  ValidationErrorCode,
} from "./types.ts";
import type {
  Message,
  MessageData,
  ValidationDeps,
  ValidationResult,
} from "./types.ts";

/** 时间戳允许超出当前时间的最大秒数（10 分钟）。 */
export const FUTURE_LEEWAY_SECONDS = 10 * 60;

export function bytesToHex(bytes: Uint8Array): string {
  let hex = "";
  for (const b of bytes) hex += b.toString(16).padStart(2, "0");
  return hex;
}

function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

/** 结构化字段与 data_bytes 重新解码结果的深度一致性比较。 */
export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (a instanceof Uint8Array && b instanceof Uint8Array) {
    return bytesEqual(a, b);
  }
  if (
    typeof a !== "object" ||
    typeof b !== "object" ||
    a === null ||
    b === null
  ) {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) {
      return false;
    }
    return a.every((v, i) => deepEqual(v, b[i]));
  }
  const aRec = a as Record<string, unknown>;
  const bRec = b as Record<string, unknown>;
  const aKeys = Object.keys(aRec).filter((k) => aRec[k] !== undefined);
  const bKeys = Object.keys(bRec).filter((k) => bRec[k] !== undefined);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((k) => deepEqual(aRec[k], bRec[k]));
}

interface SignerState {
  /** 最近一次 SignerAdd 的时间戳；从未添加为 undefined。 */
  addedAt?: number;
  /** 最近一次 SignerRemove 的时间戳；从未移除为 undefined。 */
  removedAt?: number;
}

function fail(
  code: ValidationErrorCode,
  reason: string,
): ValidationResult {
  return { ok: false, code, reason };
}

const OK: ValidationResult = { ok: true };

/**
 * 供消息入库调用的校验器。每个 fid 维护签名者事件与已入库消息，
 * 所有密码学原语（哈希、验签、解码）由调用方注入。
 */
export class HubValidator {
  private readonly deps: ValidationDeps;
  /** fid -> signerHex -> 签名者事件状态。 */
  private readonly signers = new Map<number, Map<string, SignerState>>();
  /** fid -> 已入库消息。 */
  private readonly store = new Map<number, Message[]>();
  /** fid -> 因 SignerRemove 被逐出的消息（供审计/测试）。 */
  private readonly evicted = new Map<number, Message[]>();

  constructor(deps: ValidationDeps) {
    this.deps = deps;
  }

  /** 签名者当前是否活跃：已添加且未被不晚于添加时间的移除覆盖。平局时移除胜出。 */
  isActiveSigner(fid: number, signer: Uint8Array): boolean {
    const state = this.signers.get(fid)?.get(bytesToHex(signer));
    if (!state || state.addedAt === undefined) return false;
    if (state.removedAt === undefined) return true;
    return state.addedAt > state.removedAt;
  }

  getMessages(fid: number): Message[] {
    return [...(this.store.get(fid) ?? [])];
  }

  getEvictedMessages(fid: number): Message[] {
    return [...(this.evicted.get(fid) ?? [])];
  }

  /** 校验消息，全部通过后入库并应用签名者状态变更。 */
  validateAndStore(message: Message): ValidationResult {
    const structural = this.checkTypeBodyMatch(message.data);
    if (!structural.ok) return structural;

    const integrity = this.checkHashAndBytes(message);
    if (!integrity.ok) return integrity;

    const timestamp = this.checkTimestamp(message.data);
    if (!timestamp.ok) return timestamp;

    if (!this.deps.verifySignature(message)) {
      return fail(
        ValidationErrorCode.INVALID_SIGNATURE,
        "signature is not valid for signer and data_bytes",
      );
    }

    const authorized = this.checkSignerAuthorized(message);
    if (!authorized.ok) return authorized;

    this.applySignerEffects(message);
    this.storeMessage(message);
    return OK;
  }

  /** 消息类型必须与正文种类一一对应，且正文恰好设置一个字段。 */
  private checkTypeBodyMatch(data: MessageData): ValidationResult {
    const expectedKind = TYPE_TO_BODY_KIND[data.type];
    if (expectedKind === undefined) {
      return fail(
        ValidationErrorCode.TYPE_BODY_MISMATCH,
        `unknown message type ${data.type}`,
      );
    }
    const body = data.body ?? {};
    const presentKinds = (
      Object.keys(body) as (keyof typeof body)[]
    ).filter((k) => body[k] !== undefined);
    if (presentKinds.length !== 1 || presentKinds[0] !== expectedKind) {
      return fail(
        ValidationErrorCode.TYPE_BODY_MISMATCH,
        `message type ${data.type} requires body.${expectedKind}, ` +
          `got [${presentKinds.join(", ")}]`,
      );
    }
    return OK;
  }

  /** hash 必须等于对 data_bytes 的计算结果，且 data_bytes 解码后与结构化字段一致。 */
  private checkHashAndBytes(message: Message): ValidationResult {
    const computed = this.deps.hashData(message.dataBytes);
    if (!bytesEqual(computed, message.hash)) {
      return fail(
        ValidationErrorCode.INVALID_HASH,
        "message hash does not equal hash(data_bytes)",
      );
    }
    let decoded: MessageData;
    try {
      decoded = this.deps.decodeData(message.dataBytes);
    } catch (err) {
      return fail(
        ValidationErrorCode.MALFORMED_DATA_BYTES,
        `data_bytes failed to decode: ${(err as Error).message}`,
      );
    }
    if (!deepEqual(decoded, message.data)) {
      return fail(
        ValidationErrorCode.DATA_BYTES_MISMATCH,
        "data_bytes re-decoded data differs from structured fields",
      );
    }
    return OK;
  }

  private checkTimestamp(data: MessageData): ValidationResult {
    const limit = this.deps.now() + FUTURE_LEEWAY_SECONDS;
    if (data.timestamp > limit) {
      return fail(
        ValidationErrorCode.TIMESTAMP_TOO_FAR_FUTURE,
        `timestamp ${data.timestamp} is more than 10 minutes ahead of now`,
      );
    }
    return OK;
  }

  /**
   * SignerAdd/SignerRemove 可由 custody 地址或当前活跃签名者签发；
   * 其余消息必须由当前活跃签名者签发。
   */
  private checkSignerAuthorized(message: Message): ValidationResult {
    const { fid, type } = message.data;
    const isSignerManagement =
      type === MessageType.SIGNER_ADD || type === MessageType.SIGNER_REMOVE;
    if (this.isActiveSigner(fid, message.signer)) return OK;
    if (
      isSignerManagement &&
      this.deps.isCustodySigner(fid, message.signer)
    ) {
      return OK;
    }
    return fail(
      ValidationErrorCode.INACTIVE_SIGNER,
      "signer is not in the fid's active signer set",
    );
  }

  /**
   * 应用签名者事件：记录 add/remove 时间戳（平局移除胜出由
   * isActiveSigner 的严格大于保证）；SignerRemove 生效时把该签名者
   * 此前签发的全部已入库消息逐出。
   */
  private applySignerEffects(message: Message): void {
    const { fid, type, timestamp, body } = message.data;
    if (type === MessageType.SIGNER_ADD && body.signerAdd) {
      const state = this.signerState(fid, body.signerAdd.signer);
      state.addedAt = Math.max(state.addedAt ?? -1, timestamp);
    } else if (type === MessageType.SIGNER_REMOVE && body.signerRemove) {
      const target = body.signerRemove.signer;
      const state = this.signerState(fid, target);
      state.removedAt = Math.max(state.removedAt ?? -1, timestamp);
      this.evictMessagesOfSigner(fid, target);
    }
  }

  private evictMessagesOfSigner(fid: number, signer: Uint8Array): void {
    const messages = this.store.get(fid);
    if (!messages) return;
    const targetHex = bytesToHex(signer);
    const kept: Message[] = [];
    for (const m of messages) {
      if (bytesToHex(m.signer) === targetHex) {
        const evictedList = this.evicted.get(fid) ?? [];
        evictedList.push(m);
        this.evicted.set(fid, evictedList);
      } else {
        kept.push(m);
      }
    }
    this.store.set(fid, kept);
  }

  private signerState(fid: number, signer: Uint8Array): SignerState {
    let fidSigners = this.signers.get(fid);
    if (!fidSigners) {
      fidSigners = new Map();
      this.signers.set(fid, fidSigners);
    }
    const key = bytesToHex(signer);
    let state = fidSigners.get(key);
    if (!state) {
      state = {};
      fidSigners.set(key, state);
    }
    return state;
  }

  private storeMessage(message: Message): void {
    const messages = this.store.get(message.data.fid) ?? [];
    messages.push(message);
    this.store.set(message.data.fid, messages);
  }
}
