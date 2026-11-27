/**
 * 领域类型：所有权判定包的公开数据结构。
 *
 * 设计约定：
 * - 对象身份 = (origin 原始签发域, permanentId 永久 ID)，两者均不可变；
 *   displayUrl 只是可替换的展示位置，不参与身份判定。
 * - 授权签名由持有人/成员密钥产生，覆盖规范化事件体。transfer 必须同时携带
 *   旧持有人与接收者两份签名，且事件体包含精确父转让哈希 prev 与代次 gen。
 * - HTTP 传输签名只证明投递来源，永远不构成编辑/所有权授权。
 */

/** 不可变对象身份：绑定原始签发域与永久 ID。 */
export interface ObjectId {
  /** 原始签发域（对象首次签发的权威域），永不改变。 */
  readonly origin: string;
  /** 永久 ID，在 origin 内唯一且永不复用。 */
  readonly permanentId: string;
}

/** 规范化身份字符串，用作分组键。 */
export function objectIdKey(id: ObjectId): string {
  return `${id.origin}#${id.permanentId}`;
}

/** 可更新的数据字段（不含身份字段与 displayUrl）。 */
export type FieldPatch = Readonly<Record<string, unknown>>;

export type EventType =
  | 'create'
  | 'update'
  | 'transfer'
  | 'revoke'
  | 'rehost'
  | 'addAlias'
  | 'addKeyAlias'
  | 'delete'
  | 'recovery';

interface EventBase {
  readonly type: EventType;
  readonly id: ObjectId;
  /** 精确前驱节点哈希：transfer 指向上一次转让；其他事件指向当前权威锚点。 */
  readonly prev: string | null;
  /** 单调代次；对象从 0 开始，每次转让 +1，同代次事件共享 gen。 */
  readonly gen: number;
}

export interface CreateEvent extends EventBase {
  readonly type: 'create';
  readonly prev: null;
  readonly gen: 0;
  readonly initialHolderKeyId: string;
  /** 可选：父对象身份，用于后代隔离（父对象分叉时其子树被隔离）。 */
  readonly parentObject?: ObjectId;
  /** 事先承诺的恢复策略；create 之后不可变更。 */
  readonly recoveryPolicy: RecoveryPolicy;
  readonly displayUrl: string;
  readonly fields?: FieldPatch;
  /** 可选：初始别名集合。 */
  readonly aliases?: readonly string[];
}

export interface UpdateEvent extends EventBase {
  readonly type: 'update';
  /** 合并或替换的数据字段。 */
  readonly fields: FieldPatch;
}

export interface TransferEvent extends EventBase {
  readonly type: 'transfer';
  /** 旧持有人密钥 ID（须为当前持有人）。 */
  readonly fromKeyId: string;
  /** 接收者密钥 ID（成为新持有人）。 */
  readonly toKeyId: string;
  /** 新展示位置；可为 null 表示沿用。 */
  readonly displayUrl: string | null;
}

export interface RevokeEvent extends EventBase {
  readonly type: 'revoke';
  /** 被撤回的密钥 ID。 */
  readonly targetKeyId: string;
  /** 'edit' 仅撤回编辑权；'all' 同时撤回编辑权与恢复投票资格。 */
  readonly scope: 'edit' | 'all';
}

export interface RehostEvent extends EventBase {
  readonly type: 'rehost';
  /** 新展示 URL；对象身份不变。 */
  readonly displayUrl: string;
}

export interface AddAliasEvent extends EventBase {
  readonly type: 'addAlias';
  readonly alias: string;
}

export interface AddKeyAliasEvent extends EventBase {
  readonly type: 'addKeyAlias';
  /** 既有（可能已轮换）密钥 ID。 */
  readonly existingKeyId: string;
  /** 新公钥对应的密钥 ID。 */
  readonly newKeyId: string;
}

export interface DeleteEvent extends EventBase {
  readonly type: 'delete';
  readonly reason?: string;
}

/** 恢复投票：一名成员对一次恢复的背书。 */
export interface RecoveryVote {
  /** 投票成员密钥 ID。 */
  readonly memberKeyId: string;
  /** 该成员对 recovery 事件体的授权签名。 */
  readonly signature: string;
}

export interface RecoveryEvent extends EventBase {
  readonly type: 'recovery';
  /** 分叉点：两支共同的父转让哈希。 */
  readonly forkParent: string;
  /** 被合并的两支末端转让哈希（恰好两支）。 */
  readonly branchTips: readonly [string, string];
  /** 选定为权威延续的那一支末端。 */
  readonly canonicalTip: string;
  /** 覆盖两支的成员投票，数量须达到 recoveryPolicy.threshold。 */
  readonly votes: readonly RecoveryVote[];
}

/** 恢复主张体：投票签名覆盖的规范化字节（不含 votes 自身，避免自引用）。 */
export type RecoveryClaim = Omit<RecoveryEvent, 'votes'>;

export type OwnershipEvent =
  | CreateEvent
  | UpdateEvent
  | TransferEvent
  | RevokeEvent
  | RehostEvent
  | AddAliasEvent
  | AddKeyAliasEvent
  | DeleteEvent
  | RecoveryEvent;

/** 事先承诺的恢复门槛。 */
export interface RecoveryPolicy {
  /** 所需的不同成员投票数（含）。 */
  readonly threshold: number;
  /** 承诺的成员密钥 ID 集合（含历史持有人）。 */
  readonly members: readonly string[];
}

/** 授权签名条目。 */
export interface AuthSignature {
  readonly keyId: string;
  /** base64url 签名，对规范化事件体签名。 */
  readonly signature: string;
}

/**
 * 联邦收件箱信封。eventHash 由规范化事件体派生；同一 eventHash 经任意投递者
 * 任意次转发都视为同一事件。transport 信息不参与身份与授权判定。
 */
export interface Envelope {
  readonly body: OwnershipEvent;
  /** 授权签名：transfer 需两条（旧持有人 + 接收者），其余需一条。 */
  readonly auth: readonly AuthSignature[];
  /** 派生字段：sha256(canonical(body)) 的 base64url。 */
  readonly eventHash: string;
  /** HTTP 投递来源（仅审计用途）。 */
  readonly delivery?: {
    readonly relay?: string;
    /** 传输层签名，只证明投递来源，不证明任何所有权操作。 */
    readonly transportSignature?: string;
  };
  /** 到达收件箱的逻辑时间（毫秒），用于乱序到达与时点查询。 */
  readonly arrivedAt: number;
}

export type DecisionKind = 'publish' | 'reject' | 'wait';

export type DecisionReason =
  | 'ok'
  | 'duplicate'
  | 'wait_missing_ancestor'
  | 'wait_unknown_key'
  | 'wait_fork_unresolved'
  | 'wait_descendant_isolated'
  | 'wait_recovery_threshold'
  | 'reject_bad_signature'
  | 'reject_unauthorized_signer'
  | 'reject_transport_only'
  | 'reject_stale_gen'
  | 'reject_unknown_origin'
  | 'reject_unknown_object'
  | 'reject_revoked_editor'
  | 'reject_fully_revoked_member'
  | 'reject_already_deleted'
  | 'reject_alias_taken'
  | 'reject_not_a_fork'
  | 'reject_recovery_membership'
  | 'reject_canonical_tip'
  | 'reject_malformed';

/** 单个信封在某一时点的判定。 */
export interface EventDecision {
  readonly eventHash: string;
  readonly kind: DecisionKind;
  readonly reason: DecisionReason;
  /** kind 为 wait 时，仍需等待的具体证据。 */
  readonly awaiting?: readonly string[];
  /** 完整事件引用（无缓存：始终回指完整信封的哈希与前驱）。 */
  readonly ref: {
    readonly eventHash: string;
    readonly prev: string | null;
    readonly gen: number;
  };
}

/** 同代次异摘要记录：检查点后识别分叉/竞争的依据。 */
export interface GenDigest {
  readonly gen: number;
  readonly digests: readonly string[];
}

/** 受信检查点：对已确认前缀的压缩。 */
export interface Checkpoint {
  readonly object: ObjectId;
  /** 已压缩到的权威事件哈希。 */
  readonly tipHash: string;
  readonly gen: number;
  readonly holderKeyId: string;
  readonly displayUrl: string;
  readonly aliases: readonly string[];
  /** 已存在的全部同代异摘要（保证压缩后仍能识别分叉）。 */
  readonly genDigests: readonly GenDigest[];
  /** 撤回编辑权的密钥集合。 */
  readonly revokedEdit: readonly string[];
  /** 完全撤回（含投票资格）的密钥集合。 */
  readonly revokedAll: readonly string[];
  readonly recoveryPolicy: RecoveryPolicy;
  readonly deleted: boolean;
  /** 检查点本身的受信签名（由检查点签发密钥产生）。 */
  readonly checkpointSignature?: string;
}

/** 某对象在某一时点的可发布视图。 */
export interface ObjectView {
  readonly id: ObjectId;
  readonly holderKeyId: string | null;
  readonly displayUrl: string | null;
  readonly aliases: readonly string[];
  readonly fields: FieldPatch;
  readonly gen: number;
  /** 当前是否处于未解决分叉（其后代被隔离）。 */
  readonly forked: boolean;
  readonly deleted: boolean;
  /** 该视图依据的完整权威事件哈希链（无缓存、可回放）。 */
  readonly authorityChain: readonly string[];
}

export interface Resolution {
  readonly decisions: readonly EventDecision[];
  readonly views: ReadonlyMap<string, ObjectView>;
  /** 被隔离（含其自身）的对象键：未解决分叉对象的后代子树。 */
  readonly isolated: readonly string[];
  /** 去重计数：同一 eventHash 被不同投递者重复投递的次数。 */
  readonly duplicateDeliveries: number;
}

/** 密钥解析器：把 keyId 解析为可验签的公钥材料。 */
export interface KeyResolver {
  /** 返回 base64url 公钥（Ed25519 原始 32 字节）；未知则返回 null。 */
  resolveKey(keyId: string): string | null;
}

/** 别名/URL 索引条目：同一 URL 可对应两个不同对象。 */
export interface LookupEntry {
  readonly objectKey: string;
  readonly kind: 'url' | 'alias';
  readonly value: string;
}
