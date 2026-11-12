/**
 * 滚动列表增量抽取包的公共类型定义。
 *
 * 设计前提：虚拟列表会回收并复用 DOM 节点，因此 DOM 节点 ID 只在单轮快照内
 * 有意义，绝不能作为跨轮次的记录身份。记录身份始终由可信业务键承载。
 */

/** 业务键：只允许字符串或数字，保证可稳定序列化与做 Map 键。 */
export type BusinessKey = string | number;

/** 单轮滚动后采集到的一行可见行快照。 */
export interface VisibleRow<K extends BusinessKey = BusinessKey> {
  /** 当前承载该行的 DOM 节点 ID，仅本轮有效，可能被回收复用。 */
  readonly nodeId: string;
  /** 可信业务键，记录的唯一身份。 */
  readonly key: K;
  /** 业务内容（任意可结构化克隆的数据）。 */
  readonly content: unknown;
  /** 宿主给出的渲染代次，用于区分内容修订。 */
  readonly renderGeneration: number;
}

/** 一次滚动后提交给抽取器的整轮可见行快照。 */
export interface ScrollSnapshot<K extends BusinessKey = BusinessKey> {
  /** 单调递增的滚动轮次序号（从 0 开始）。 */
  readonly round: number;
  /** 当前视口内的可见行，顺序即视觉自上而下的顺序。 */
  readonly rows: ReadonlyArray<VisibleRow<K>>;
  /**
   * 宿主是否已观察到显式结束标记（如列表底部的 end-of-list 哨兵）。
   * 结束标记本身不构成完成，还需边界键连续两轮稳定。
   */
  readonly endMarked: boolean;
}

/** 一条记录在视口中的一次连续出现区间（按轮次计）。 */
export interface AppearanceInterval {
  /** 进入视口的轮次。 */
  readonly fromRound: number;
  /** 离开视口（变为不可见）的轮次；仍可见时为 null。 */
  readonly toRound: number | null;
}

/** 一条业务记录的一个内容版本。 */
export interface RecordVersion {
  /** 该版本首次观察到的轮次。 */
  readonly round: number;
  /** 该版本对应的渲染代次。 */
  readonly renderGeneration: number;
  /** 内容指纹，用于判断内容是否实质变化。 */
  readonly contentHash: string;
  /** 当时的内容快照。 */
  readonly content: unknown;
}

/** 抽取器维护的一条业务记录的完整状态。 */
export interface RecordState<K extends BusinessKey = BusinessKey> {
  readonly key: K;
  /** 首个承载该记录的节点 ID（仅作诊断信息，不做身份）。 */
  readonly firstSeenNodeId: string;
  /** 最近一次承载该记录的节点 ID。 */
  readonly lastNodeId: string;
  /** 首次观察到的轮次。 */
  readonly firstSeenRound: number;
  /** 最近一次可见的轮次。 */
  readonly lastSeenRound: number;
  /** 最近一次内容指纹。 */
  readonly contentHash: string;
  /** 最近一次渲染代次。 */
  readonly renderGeneration: number;
  /** 最新内容。 */
  readonly content: unknown;
  /** 全部内容版本（含首版与每次修订）。 */
  readonly versions: ReadonlyArray<RecordVersion>;
  /** 全部出现区间；最后一个区间可能仍开放（toRound 为 null）。 */
  readonly appearances: ReadonlyArray<AppearanceInterval>;
  /** 当前是否处于视口内。 */
  readonly visible: boolean;
}

/** 一次 ingest 产出的单条变更。 */
export interface RecordChange<K extends BusinessKey = BusinessKey> {
  readonly key: K;
  /** inserted：业务键首次出现；revised：同键内容变化；reseen：离开后再次出现。 */
  readonly kind: "inserted" | "revised" | "reseen";
  readonly round: number;
  readonly nodeId: string;
  readonly previousNodeId: string | null;
  readonly renderGeneration: number;
  readonly previousContentHash: string | null;
  readonly contentHash: string;
  readonly content: unknown;
}

/** 完成判定证据。 */
export interface CompletionEvidence<K extends BusinessKey = BusinessKey> {
  /** 达到稳定的轮次。 */
  readonly round: number;
  /** 稳定的首行边界键。 */
  readonly firstKey: K | null;
  /** 稳定的末行边界键。 */
  readonly lastKey: K | null;
  /** 显式结束标记已连续出现的轮次数（>=2）。 */
  readonly endMarkedStreak: number;
  /** 边界键保持不变的连续轮次数（>=2）。 */
  readonly boundaryStableStreak: number;
}

/**
 * 未完成证据。
 * - end-not-marked：尚未观察到显式结束标记
 * - boundary-changed：结束标记已出现但边界键仍在移动
 * - boundary-revisit：边界键回到历史值，疑似循环滚动
 * - wrap-seam：同一快照内发现顺序回绕接缝，疑似循环/无限列表
 */
export type IncompleteReason =
  | "end-not-marked"
  | "boundary-changed"
  | "boundary-revisit"
  | "wrap-seam";

export interface IncompleteEvidence<K extends BusinessKey = BusinessKey> {
  readonly round: number;
  readonly reasons: ReadonlyArray<IncompleteReason>;
  /** 本轮首行边界键。 */
  readonly firstKey: K | null;
  /** 本轮末行边界键。 */
  readonly lastKey: K | null;
  /** wrap-seam 时，回绕接缝处的相邻键对（前 -> 后）。 */
  readonly wrapSeam: { readonly before: K; readonly after: K } | null;
  /** boundary-revisit 时，历史上曾出现过相同边界的最早轮次。 */
  readonly revisitedAtRound: number | null;
}

/** 单轮快照处理结果。 */
export interface IngestResult<K extends BusinessKey = BusinessKey> {
  readonly round: number;
  /** 本轮产生的变更（按可见行顺序）。 */
  readonly changes: ReadonlyArray<RecordChange<K>>;
  /** 本轮仍可见的键集合。 */
  readonly visibleKeys: ReadonlyArray<K>;
  /** 本轮从可见变为不可见的键（不删除，仅关闭出现区间）。 */
  readonly hiddenKeys: ReadonlyArray<K>;
  /** 本轮检测到的节点换键（同一 nodeId 改绑别的业务键）。 */
  readonly nodeRebinds: ReadonlyArray<{
    readonly nodeId: string;
    readonly fromKey: K;
    readonly toKey: K;
  }>;
  readonly complete: boolean;
  readonly completionEvidence: CompletionEvidence<K> | null;
  readonly incompleteEvidence: IncompleteEvidence<K> | null;
}
