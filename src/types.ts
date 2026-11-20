/**
 * 流状态冲突解决所需的领域类型。
 *
 * 本包不依赖具体的 CID 实现：CID 在接口层统一表示为字符串，
 * 调用方负责保证同一 CID 始终以相同的规范字符串形式传入，
 * 比较一律按字符串字典序进行。
 */

/** 锚定凭据（例如 Ceramic 锚定服务返回的 AnchorProof）。本包不解析其内部结构。 */
export type AnchorProof = unknown;

/** 流上的一次提交。 */
export interface StreamCommit {
  /** 提交自身的 CID（规范字符串）。 */
  cid: string;
  /** 父提交 CID；创世提交的父 CID 为 undefined。 */
  prev?: string;
  /**
   * 该提交携带的锚定凭据。没有锚定时为 undefined。
   * 凭据是否有效由调用方注入的 AnchorVerifier 决定。
   */
  anchor?: AnchorProof;
}

/** 一条候选历史：从同一创世提交出发的有序提交链（含创世提交）。 */
export interface Branch {
  /** 该支所属流的创世提交 CID。 */
  genesisCid: string;
  /** 提交链，按时间从旧到新排列，末项为提示提交。 */
  commits: StreamCommit[];
}

/** 经校验后的锚定信息。未通过校验或没有凭据时 anchorTimestamp 为 undefined。 */
export interface AnchorInfo {
  /** 锚定区块时间戳（Unix 秒或毫秒均可，全流程保持同一量纲即可）。 */
  anchorTimestamp?: number;
}

/**
 * 锚定凭据验证器，由调用方注入。
 * 抛异常或返回 undefined 均视为“未通过验证”，该锚定按未锚定处理。
 */
export type AnchorVerifier = (
  proof: AnchorProof,
  commitCid: string,
  branch: Branch,
) => AnchorInfo | undefined | Promise<AnchorInfo | undefined>;

/** 冲突解决结果。 */
export interface ResolutionResult {
  /** 胜出的分支。 */
  winner: Branch;
  /** 被淘汰的分支。 */
  loser: Branch;
  /** 胜出分支的提示 CID。 */
  tipCid: string;
  /** 胜出分支的长度（提交数）。 */
  tipLength: number;
  /** 胜出分支经校验后的锚定时间戳；未锚定为 undefined。 */
  anchorTimestamp?: number;
  /** 触发该结果的规则标识，便于观测与测试。 */
  reason: ResolutionReason;
}

export type ResolutionReason =
  | 'same-tip'
  | 'anchored-over-unanchored'
  | 'earlier-anchor-timestamp'
  | 'longer-history'
  | 'smaller-tip-cid'
  | 'anchor-before-current-anchor'
  | 'anchor-not-before-current-anchor'
  | 'unanchored-cannot-replace-anchored';
