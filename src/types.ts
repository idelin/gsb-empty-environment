/**
 * 公开类型定义。
 *
 * 该包在隔离的 worklet 中运行：广告脚本只能拿到 {@link ContributionGate}，
 * 报告内容只能由宿主通过创建 gate 时返回的 host-only drain 句柄取出。
 */

/** 单条直方图贡献。bucket 可能超出 Number.MAX_SAFE_INTEGER，因此必须用 bigint。 */
export interface HistogramContribution {
  /** 桶标识，[0, 2^128) 范围内的非负 bigint 整数。 */
  bucket: bigint;
  /** 贡献值，非负安全整数（number）。 */
  value: number;
  /** 可选的单条 filtering_id 覆盖值，范围 [0, 255]。 */
  filteringId?: number;
}

/** 一次 contributeToHistogram 调用的选项。 */
export interface ContributionOptions {
  /** 宿主登记白名单中的聚合协调方 origin。 */
  aggregationCoordinatorOrigin: string;
  /** 上下文标识；同一 context_id 的贡献汇聚到同一份报告。 */
  contextId: string;
  /** 整份报告统一的 filtering_id，范围 [0, 255]，默认 0。 */
  filteringId?: number;
  /** 调用方是否请求调试模式；仅在第三方 Cookie 可用时才真正生效。 */
  debugMode?: boolean;
}

/** 宿主提供的环境能力。 */
export interface HostCapabilities {
  /** 第三方 Cookie 当前是否可用。 */
  thirdPartyCookiesEnabled: boolean;
}

/** createContributionGate 的配置。 */
export interface GateConfig {
  /** 宿主登记的聚合协调方 origin 白名单。 */
  allowedCoordinatorOrigins: readonly string[];
  /** 宿主环境能力，未提供时视为第三方 Cookie 不可用。 */
  capabilities?: HostCapabilities;
  /** 单份报告允许的最大贡献条数，默认 20。 */
  maxContributionsPerReport?: number;
  /** context_id 的最大字节长度，默认 64。 */
  maxContextIdBytes?: number;
}

/** 内部存储的单条贡献（冻结快照，调用方之后的变更无法影响已入库数据）。 */
export interface StoredContribution {
  readonly bucket: bigint;
  readonly value: number;
  readonly filteringId: number;
}

/** 待加密/发送的报告内容。只能通过 host-only drain 句柄离开包。 */
export interface PreparedReport {
  readonly contextId: string;
  readonly aggregationCoordinatorOrigin: string;
  readonly filteringId: number;
  readonly debugMode: boolean;
  readonly contributions: ReadonlyArray<StoredContribution>;
}

/** drain 返回的元数据，不包含任何贡献内容。 */
export interface ReportRecord {
  readonly contextId: string;
  readonly aggregationCoordinatorOrigin: string;
  readonly filteringId: number;
  readonly debugMode: boolean;
  readonly contributionCount: number;
}

/** 暴露给 worklet 内广告脚本的门面对象。 */
export interface ContributionGate {
  /**
   * 校验并接收聚合贡献。
   *
   * 任何字段非法都会抛 TypeError，且本次调用（含批量）整体不产生任何写入；
   * 超过单份报告容量上限的贡献按顺序静默丢弃。
   */
  contributeToHistogram(
    contribution: HistogramContribution | ReadonlyArray<HistogramContribution>,
    options: ContributionOptions,
  ): void;
}

/** 宿主侧报告出口（不挂在 worklet 可见的 gate 上）。 */
export interface ReportSink {
  send(report: PreparedReport): void;
}

/** createContributionGate 返回的宿主句柄。 */
export interface HostGateHandle {
  /** 可安全暴露给 worklet 的门面对象（方法已绑定，可解构使用）。 */
  gate: ContributionGate;
  /** 取出指定 context_id 的报告并交给宿主 sink；不存在时返回 null 且不调用 sink。 */
  drainReport(contextId: string, sink: ReportSink): ReportRecord | null;
}
