/**
 * 消息模型的类型定义。
 *
 * 设计参考 Farcaster 协议：MessageData 是被哈希、被签名的负载；
 * Message 在外层携带 data_bytes、哈希值和签名信息。
 */

export enum MessageType {
  CAST_ADD = 'MESSAGE_TYPE_CAST_ADD',
  CAST_REMOVE = 'MESSAGE_TYPE_CAST_REMOVE',
  REACTION_ADD = 'MESSAGE_TYPE_REACTION_ADD',
  REACTION_REMOVE = 'MESSAGE_TYPE_REACTION_REMOVE',
  LINK_ADD = 'MESSAGE_TYPE_LINK_ADD',
  LINK_REMOVE = 'MESSAGE_TYPE_LINK_REMOVE',
  USER_DATA_ADD = 'MESSAGE_TYPE_USER_DATA_ADD',
  VERIFICATION_ADD = 'MESSAGE_TYPE_VERIFICATION_ADD',
  VERIFICATION_REMOVE = 'MESSAGE_TYPE_VERIFICATION_REMOVE',
  SIGNER_ADD = 'MESSAGE_TYPE_SIGNER_ADD',
  SIGNER_REMOVE = 'MESSAGE_TYPE_SIGNER_REMOVE',
}

/** 正文（body）的种类标签，与 MessageType 一一对应。 */
export enum BodyKind {
  CAST_ADD = 'CAST_ADD',
  CAST_REMOVE = 'CAST_REMOVE',
  REACTION_ADD = 'REACTION_ADD',
  REACTION_REMOVE = 'REACTION_REMOVE',
  LINK_ADD = 'LINK_ADD',
  LINK_REMOVE = 'LINK_REMOVE',
  USER_DATA_ADD = 'USER_DATA_ADD',
  VERIFICATION_ADD = 'VERIFICATION_ADD',
  VERIFICATION_REMOVE = 'VERIFICATION_REMOVE',
  SIGNER_ADD = 'SIGNER_ADD',
  SIGNER_REMOVE = 'SIGNER_REMOVE',
}

/** 消息类型 -> 该类型允许携带的正文种类。 */
export const MESSAGE_BODY_KIND: Readonly<Record<MessageType, BodyKind>> = {
  [MessageType.CAST_ADD]: BodyKind.CAST_ADD,
  [MessageType.CAST_REMOVE]: BodyKind.CAST_REMOVE,
  [MessageType.REACTION_ADD]: BodyKind.REACTION_ADD,
  [MessageType.REACTION_REMOVE]: BodyKind.REACTION_REMOVE,
  [MessageType.LINK_ADD]: BodyKind.LINK_ADD,
  [MessageType.LINK_REMOVE]: BodyKind.LINK_REMOVE,
  [MessageType.USER_DATA_ADD]: BodyKind.USER_DATA_ADD,
  [MessageType.VERIFICATION_ADD]: BodyKind.VERIFICATION_ADD,
  [MessageType.VERIFICATION_REMOVE]: BodyKind.VERIFICATION_REMOVE,
  [MessageType.SIGNER_ADD]: BodyKind.SIGNER_ADD,
  [MessageType.SIGNER_REMOVE]: BodyKind.SIGNER_REMOVE,
};

export enum HashScheme {
  BLAKE3 = 'HASH_SCHEME_BLAKE3',
  SHA256 = 'HASH_SCHEME_SHA256',
}

export enum SignatureScheme {
  ED25519 = 'SIGNATURE_SCHEME_ED25519',
}

export interface CastAddBody {
  bodyType: BodyKind.CAST_ADD;
  text: string;
  mentions: number[];
  parentUrl?: string;
}

export interface CastRemoveBody {
  bodyType: BodyKind.CAST_REMOVE;
  targetHash: Uint8Array;
}

export interface ReactionBody {
  bodyType: BodyKind.REACTION_ADD | BodyKind.REACTION_REMOVE;
  reactionType: number;
  targetCastHash: Uint8Array;
}

export interface LinkBody {
  bodyType: BodyKind.LINK_ADD | BodyKind.LINK_REMOVE;
  targetFid: number;
}

export interface UserDataBody {
  bodyType: BodyKind.USER_DATA_ADD;
  dataType: number;
  value: string;
}

export interface VerificationBody {
  bodyType: BodyKind.VERIFICATION_ADD | BodyKind.VERIFICATION_REMOVE;
  claim: Uint8Array;
}

export interface SignerAddBody {
  bodyType: BodyKind.SIGNER_ADD;
  /** 被授权的签名者公钥（去重标识）。 */
  signer: Uint8Array;
  name?: string;
}

export interface SignerRemoveBody {
  bodyType: BodyKind.SIGNER_REMOVE;
  /** 被移除的签名者公钥。 */
  signer: Uint8Array;
}

export type MessageBody =
  | CastAddBody
  | CastRemoveBody
  | ReactionBody
  | LinkBody
  | UserDataBody
  | VerificationBody
  | SignerAddBody
  | SignerRemoveBody;

/** 被哈希与签名的结构化消息负载。 */
export interface MessageData {
  type: MessageType;
  fid: number;
  timestamp: number;
  network: number;
  body: MessageBody;
}

export interface Message {
  data: MessageData;
  /** data 的规范编码字节，哈希与签名的输入。 */
  dataBytes: Uint8Array;
  hash: Uint8Array;
  hashScheme: HashScheme;
  signature: Uint8Array;
  signatureScheme: SignatureScheme;
  /** 签发该消息的公钥：普通消息为活跃签名者，签名者管理消息为 fid 的托管密钥。 */
  signer: Uint8Array;
}
