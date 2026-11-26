/**
 * 联邦社交收件箱所有权判定组件 —— 领域类型定义。
 *
 * 设计要点：
 * - 对象身份由永久 ID + 原始签发域绑定，绝不参与任何域名排序式分叉裁决。
 * - 所有权限判定只承认所有权链上的密码学签名；HTTP 投递签名仅作传输参考。
 * - 事件以乱序方式进入引擎，缺失前置依赖的事件进入挂起缓冲而非立即拒绝。
 */

/** 永久对象标识（生命周期内不变）。 */
export type ObjectId = string;
/** 行为体规范标识，例如 acct:alice@origin.example。 */
export type ActorId = string;
/** 链节点（根登记 / 转让 / 恢复）标识。 */
export type ChainNodeId = string;
/** 委托授权标识。 */
export type GrantId = string;
/** 事件标识，全局幂等键。 */
export type EventId = string;
/** 行为体公钥版本标识。 */
export type KeyId = string;
/** 防重放随机数（按对象隔离记账）。 */
export type Nonce = string;

/** 裁决结论：允许 / 拒绝 / 挂起。 */
export type Verdict = 'allow' | 'deny' | 'suspend';

/**
 * 证据条目。每一次裁决都输出可审计的证据清单，
 * 冲突影响严格限制在单个对象内部。
 */
export interface Evidence {
  code: string;
  verdict: Verdict;
  message: string;
  objectId: ObjectId;
  eventId?: EventId;
  detail?: Readonly<Record<string, unknown>>;
}

/** 公钥版本（base64url 编码的 SPKI Ed25519 公钥）。 */
export interface PublicKeyInfo {
  keyId: KeyId;
  publicKey: string;
  validFrom: number;
  validAfter?: number;
  revokedAt?: number;
}

/** 别名描述：行为体可绑定历史别名，别名可退役且退役后永久隔离。 */
export interface AliasInfo {
  alias: ActorId;
  canonical: ActorId;
  boundAt: number;
  revokedAt?: number;
}

/** 对象登记事件负载。 */
export interface ObjectRegistrationPayload {
  kind: 'object.registration';
  objectId: ObjectId;
  originDomain: string;
  permanentId: string;
  initialOwner: ActorId;
  initialKey: PublicKeyInfo;
  nonce: Nonce;
}

/** 发布新公钥版本（密钥轮换，旧版本永久留痕）。 */
export interface KeyPublishPayload {
  kind: 'key.publish';
  objectId: ObjectId;
  actor: ActorId;
  key: PublicKeyInfo;
  nonce: Nonce;
  signature: string;
  keyId: KeyId;
}

/** 撤销某公钥版本（不可逆）。 */
export interface KeyRevokePayload {
  kind: 'key.revoke';
  objectId: ObjectId;
  actor: ActorId;
  revokedKeyId: KeyId;
  revokedAt: number;
  nonce: Nonce;
  signature: string;
  keyId: KeyId;
}

/** 绑定别名。 */
export interface AliasBindPayload {
  kind: 'alias.bind';
  objectId: ObjectId;
  owner: ActorId;
  alias: ActorId;
  aliasKey: PublicKeyInfo;
  nonce: Nonce;
  signature: string;
  keyId: KeyId;
}

/** 退役别名（不可逆，退役后旧别名不得再发布任何字段）。 */
export interface AliasRetirePayload {
  kind: 'alias.retire';
  objectId: ObjectId;
  owner: ActorId;
  alias: ActorId;
  retiredAt: number;
  nonce: Nonce;
  signature: string;
  keyId: KeyId;
}

/**
 * 正式所有权转让：旧持有人与新接收者双签名，
 * 绑定精确父转让节点与代次。
 */
export interface OwnershipTransferPayload {
  kind: 'ownership.transfer';
  objectId: ObjectId;
  transferId: ChainNodeId;
  parentId: ChainNodeId;
  prevGeneration: number;
  oldHolder: ActorId;
  newHolder: ActorId;
  nonce: Nonce;
  oldHolderSignature: string;
  oldHolderKeyId: KeyId;
  newHolderSignature: string;
  newHolderKeyId: KeyId;
}

/**
 * 分叉恢复（合并）：签名必须覆盖当前全部活跃分叉末端，
 * 满足法定恢复门槛。域名排序永远不参与裁决。
 */
export interface ForkRecoveryPayload {
  kind: 'fork.recovery';
  objectId: ObjectId;
  recoveryId: ChainNodeId;
  tipIds: ReadonlyArray<ChainNodeId>;
  newHolder: ActorId;
  quorumSignature: ReadonlyArray<{ actor: ActorId; keyId: KeyId; signature: string }>;
  nonce: Nonce;
}

/** 委托子编辑授权：时间窗 + 字段作用域。 */
export interface DelegationGrantPayload {
  kind: 'delegation.grant';
  objectId: ObjectId;
  grantId: GrantId;
  delegator: ActorId;
  delegate: ActorId;
  fields: ReadonlyArray<string>;
  notBefore: number;
  notAfter: number;
  nonce: Nonce;
  signature: string;
  keyId: KeyId;
}

/** 撤销委托（不可逆，任何分支上的撤销在恢复后依然有效）。 */
export interface DelegationRevokePayload {
  kind: 'delegation.revoke';
  objectId: ObjectId;
  grantId: GrantId;
  revoker: ActorId;
  revokedAt: number;
  nonce: Nonce;
  signature: string;
  keyId: KeyId;
}

/** 局部字段编辑。 */
export interface FieldEditPayload {
  kind: 'field.edit';
  objectId: ObjectId;
  editId: EventId;
  editor: ActorId;
  editorKeyId: KeyId;
  field: string;
  value: unknown;
  atTime: number;
  grantId?: GrantId;
  signature: string;
  nonce: Nonce;
}

/** 检查点压缩标记事件（由引擎确定性产出，重放时强制核对）。 */
export interface CheckpointCompressPayload {
  kind: 'checkpoint.compress';
  objectId: ObjectId;
  throughAppliedSeq: number;
  stateHash: string;
}

export type AuthorityPayload =
  | ObjectRegistrationPayload
  | KeyPublishPayload
  | KeyRevokePayload
  | AliasBindPayload
  | AliasRetirePayload
  | OwnershipTransferPayload
  | ForkRecoveryPayload
  | DelegationGrantPayload
  | DelegationRevokePayload
  | FieldEditPayload;

export type AnyPayload = AuthorityPayload | CheckpointCompressPayload;

/**
 * HTTP 投递层信息。仅作传输来源参考，
 * 不构成任何所有权 / 编辑权证明。
 */
export interface TransportEnvelope {
  relayId: string;
  transportNonce: string;
  transportSignature?: string;
}

/** 事件信封：权威负载 + 可选传输参考信息。 */
export interface DomainEvent<P extends AnyPayload = AnyPayload> {
  eventId: EventId;
  timestamp: number;
  payload: P;
  transport?: TransportEnvelope;
}

/** 所有权链节点（登记根 / 转让 / 恢复）。 */
export interface ChainNode {
  nodeId: ChainNodeId;
  kind: 'root' | 'transfer' | 'recovery';
  owner: ActorId;
  generation: number;
  parentId?: ChainNodeId;
  recoveryParents?: ReadonlyArray<ChainNodeId>;
  eventId: EventId;
  atTime: number;
}

/** 委托授权记录（撤销以墓碑形式永久保留）。 */
export interface GrantRecord {
  grantId: GrantId;
  delegator: ActorId;
  delegate: ActorId;
  fields: ReadonlyArray<string>;
  notBefore: number;
  notAfter: number;
  anchorNodeId: ChainNodeId;
  status: 'active' | 'revoked';
  grantedAt: number;
  revokedAt?: number;
  revokedBy?: ActorId;
}

/** 已应用的字段编辑（检查点压缩后只保留精简骨架）。 */
export interface AppliedEdit {
  editId: EventId;
  field: string;
  editor: ActorId;
  atTime: number;
  appliedSeq: number;
  compressed?: boolean;
}

/** 单对象完整判定状态。 */
export interface ObjectState {
  objectId: ObjectId;
  originDomain: string;
  permanentId: string;
  rootEventId: EventId;
  actors: Record<ActorId, { keys: Record<KeyId, PublicKeyInfo> }>;
  aliases: Record<ActorId, AliasInfo>;
  chain: Record<ChainNodeId, ChainNode>;
  rootNodeId: ChainNodeId;
  grants: Record<GrantId, GrantRecord>;
  appliedEdits: AppliedEdit[];
  spentNonces: Set<Nonce>;
  seenEventIds: Set<EventId>;
  transportNonces: Record<string, Set<string>>;
  compressedBeforeSeq?: number;
}

/** 引擎整体状态：按对象严格隔离。 */
export interface OwnershipState {
  objects: Record<ObjectId, ObjectState>;
  /** 已成功应用的事件，按应用次序排列。 */
  appliedLog: DomainEvent[];
  /** 依赖未就绪而挂起的事件，乱序收敛的核心缓冲。 */
  pending: DomainEvent[];
  /** 被永久拒绝的事件 ID 及原因。 */
  rejected: Record<EventId, { reason: string; message: string }>;
  appliedSeq: number;
  checkpoints: Record<ObjectId, { throughAppliedSeq: number; stateHash: string }>;
}

/** 法定恢复门槛函数：输入末端数与去重签名人数，输出是否达标。 */
export type QuorumRule = (tipCount: number, distinctSignerCount: number) => boolean;

export interface EngineOptions {
  /** 默认多数门槛：floor(n/2)+1，域名排序永不参与。 */
  quorumRule?: QuorumRule;
  /** 挂起缓冲上限，防止无限内存增长。 */
  maxPending?: number;
}

/** 事件摄取结果。 */
export interface IngestResult {
  eventId: EventId;
  objectId: ObjectId;
  status: 'applied' | 'duplicate' | 'pending' | 'rejected';
  /** 字段编辑事件携带完整裁决；其他事件在拒绝/挂起时给出原因。 */
  verdict?: Verdict;
  evidence: Evidence[];
  appliedSeq?: number;
  reason?: string;
}

/** 字段编辑的独立裁决结果（纯函数，可在不摄取事件时调用）。 */
export interface EditDecision {
  verdict: Verdict;
  evidence: Evidence[];
}

/** 检查点快照（可用于从压缩点继续重放）。 */
export interface CheckpointSnapshot {
  objectId: ObjectId;
  throughAppliedSeq: number;
  stateHash: string;
  state: ObjectState;
}
