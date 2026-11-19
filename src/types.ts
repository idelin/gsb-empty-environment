// Farcaster hub 消息入库校验：类型定义。
// 注意：本包运行在 node --experimental-strip-types 下，不能使用 enum，
// 因此用 const 对象 + 联合类型代替。

export const MessageType = {
  CAST_ADD: 1,
  CAST_REMOVE: 2,
  REACTION_ADD: 3,
  REACTION_REMOVE: 4,
  LINK_ADD: 5,
  LINK_REMOVE: 6,
  VERIFICATION_ADD: 7,
  VERIFICATION_REMOVE: 8,
  USER_DATA_ADD: 9,
  SIGNER_ADD: 10,
  SIGNER_REMOVE: 11,
} as const;
export type MessageType = (typeof MessageType)[keyof typeof MessageType];

export interface CastAddBody {
  text: string;
  mentions?: number[];
  parentHash?: Uint8Array;
}

export interface CastRemoveBody {
  targetHash: Uint8Array;
}

export interface ReactionBody {
  reactionType: "like" | "recast";
  targetHash: Uint8Array;
}

export interface LinkBody {
  linkType: string;
  targetFid: number;
}

export interface VerificationBody {
  address: Uint8Array;
  claimSignature: Uint8Array;
}

export interface UserDataBody {
  userDataType: string;
  value: string;
}

export interface SignerAddBody {
  signer: Uint8Array;
}

export interface SignerRemoveBody {
  signer: Uint8Array;
}

/** 消息正文：oneof，必须恰好设置一个字段。 */
export interface MessageBody {
  castAdd?: CastAddBody;
  castRemove?: CastRemoveBody;
  reaction?: ReactionBody;
  link?: LinkBody;
  verification?: VerificationBody;
  userData?: UserDataBody;
  signerAdd?: SignerAddBody;
  signerRemove?: SignerRemoveBody;
}

export type BodyKind = keyof MessageBody;

/** 消息类型 -> 正文字段名 的对应表。 */
export const TYPE_TO_BODY_KIND: Record<MessageType, BodyKind> = {
  [MessageType.CAST_ADD]: "castAdd",
  [MessageType.CAST_REMOVE]: "castRemove",
  [MessageType.REACTION_ADD]: "reaction",
  [MessageType.REACTION_REMOVE]: "reaction",
  [MessageType.LINK_ADD]: "link",
  [MessageType.LINK_REMOVE]: "link",
  [MessageType.VERIFICATION_ADD]: "verification",
  [MessageType.VERIFICATION_REMOVE]: "verification",
  [MessageType.USER_DATA_ADD]: "userData",
  [MessageType.SIGNER_ADD]: "signerAdd",
  [MessageType.SIGNER_REMOVE]: "signerRemove",
};

export interface MessageData {
  fid: number;
  type: MessageType;
  /** Unix 秒。 */
  timestamp: number;
  body: MessageBody;
}

export interface Message {
  data: MessageData;
  /** data 的序列化字节，hash 与签名都针对它计算。 */
  dataBytes: Uint8Array;
  hash: Uint8Array;
  signature: Uint8Array;
  /** 签发者公钥。 */
  signer: Uint8Array;
}

export const ValidationErrorCode = {
  TYPE_BODY_MISMATCH: "TYPE_BODY_MISMATCH",
  INVALID_HASH: "INVALID_HASH",
  DATA_BYTES_MISMATCH: "DATA_BYTES_MISMATCH",
  TIMESTAMP_TOO_FAR_FUTURE: "TIMESTAMP_TOO_FAR_FUTURE",
  INACTIVE_SIGNER: "INACTIVE_SIGNER",
  INVALID_SIGNATURE: "INVALID_SIGNATURE",
  MALFORMED_DATA_BYTES: "MALFORMED_DATA_BYTES",
} as const;
export type ValidationErrorCode =
  (typeof ValidationErrorCode)[keyof typeof ValidationErrorCode];

export type ValidationResult =
  | { ok: true }
  | { ok: false; code: ValidationErrorCode; reason: string };

/** 验签、哈希、解码、custody 判定、时钟全部由调用方注入。 */
export interface ValidationDeps {
  /** 对 data_bytes 计算消息哈希。 */
  hashData(dataBytes: Uint8Array): Uint8Array;
  /** 把 data_bytes 解码回结构化 MessageData；无法解码时抛异常。 */
  decodeData(dataBytes: Uint8Array): MessageData;
  /** 校验 message.signature 是否为 message.signer 对 dataBytes 的有效签名。 */
  verifySignature(message: Message): boolean;
  /** 判断 signer 是否为该 fid 的 custody 地址（签名者体系的根信任）。 */
  isCustodySigner(fid: number, signer: Uint8Array): boolean;
  /** 当前时间（Unix 秒），便于测试注入。 */
  now(): number;
}
