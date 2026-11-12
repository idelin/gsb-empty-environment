import type {
  ContributionGate,
  ContributionOptions,
  GateConfig,
  HistogramContribution,
  HostGateHandle,
  PreparedReport,
  ReportRecord,
  ReportSink,
  StoredContribution,
} from './types.ts';
import {
  DEFAULT_FILTERING_ID,
  assertValidBucket,
  assertValidContextId,
  assertValidFilteringId,
  assertValidValue,
  buildOriginWhitelist,
  normalizeOrigin,
} from './validation.ts';

const DEFAULT_MAX_CONTRIBUTIONS = 20;
const DEFAULT_MAX_CONTEXT_ID_BYTES = 64;

interface ReportState {
  contextId: string;
  aggregationCoordinatorOrigin: string;
  filteringId: number;
  debugMode: boolean;
  contributions: StoredContribution[];
}

function toContributionList(
  contribution: HistogramContribution | ReadonlyArray<HistogramContribution>,
): ReadonlyArray<HistogramContribution> {
  if (Array.isArray(contribution)) {
    if (contribution.length === 0) {
      throw new TypeError('contribution list must not be empty');
    }
    return contribution;
  }
  if (contribution === null || typeof contribution !== 'object') {
    throw new TypeError('contribution must be an object or an array of objects');
  }
  return [contribution];
}

/**
 * 创建贡献准入 gate。
 *
 * 返回的 {@link HostGateHandle.gate} 可暴露给 worklet；reports 状态保存在闭包里，
 * drainReport 不挂到 gate 上，因此 worklet 侧无法观察任何已提交的贡献内容。
 */
export function createContributionGate(config: GateConfig): HostGateHandle {
  const allowedOrigins = buildOriginWhitelist(config.allowedCoordinatorOrigins);
  const capabilities = config.capabilities ?? {
    thirdPartyCookiesEnabled: false,
  };
  const maxContributions =
    config.maxContributionsPerReport ?? DEFAULT_MAX_CONTRIBUTIONS;
  const maxContextIdBytes = config.maxContextIdBytes ?? DEFAULT_MAX_CONTEXT_ID_BYTES;

  if (
    typeof maxContributions !== 'number' ||
    !Number.isInteger(maxContributions) ||
    maxContributions <= 0
  ) {
    throw new TypeError('maxContributionsPerReport must be a positive integer');
  }

  const reports = new Map<string, ReportState>();

  function contributeToHistogram(
    contribution: HistogramContribution | ReadonlyArray<HistogramContribution>,
    options: ContributionOptions,
  ): void {
    if (options === null || typeof options !== 'object') {
      throw new TypeError('options must be an object');
    }

    // 1. 协调方 origin 必须命中宿主白名单，未登记则整个调用拒绝。
    const coordinatorOrigin = normalizeOrigin(
      options.aggregationCoordinatorOrigin,
    );
    if (!allowedOrigins.has(coordinatorOrigin)) {
      throw new TypeError(
        `aggregation_coordinator_origin is not allowlisted: ${coordinatorOrigin}`,
      );
    }

    // 2. context_id 形态与字节长度。
    assertValidContextId(options.contextId, maxContextIdBytes);

    // 3. 报告级 filtering_id（单条覆盖值在逐条校验时处理）。
    const reportFilteringId =
      options.filteringId ?? DEFAULT_FILTERING_ID;
    assertValidFilteringId(reportFilteringId);

    // 4. 逐条校验全部输入；任一非法则整体抛错且不写入任何内容。
    const inputList = toContributionList(contribution);
    const accepted: StoredContribution[] = [];
    for (const item of inputList) {
      if (item === null || typeof item !== 'object') {
        throw new TypeError('each contribution must be an object');
      }
      assertValidBucket(item.bucket);
      assertValidValue(item.value);
      const effectiveFilteringId =
        item.filteringId ?? reportFilteringId;
      assertValidFilteringId(effectiveFilteringId);
      accepted.push({
        bucket: item.bucket,
        value: item.value,
        filteringId: effectiveFilteringId,
      });
    }

    // 同一次调用内 filtering_id 必须一致，报告级显式声明也不得与覆盖值冲突。
    const batchFilteringId = accepted[0].filteringId;
    for (const stored of accepted) {
      if (stored.filteringId !== batchFilteringId) {
        throw new TypeError(
          'all contributions in one report must use the same filtering_id',
        );
      }
    }
    if (
      options.filteringId !== undefined &&
      options.filteringId !== batchFilteringId
    ) {
      throw new TypeError(
        'all contributions in one report must use the same filtering_id',
      );
    }

    // 5. 取/建该 context_id 的报告，并校验跨调用一致性（全部通过后才写入 map）。
    const existing = reports.get(options.contextId);
    if (existing !== undefined) {
      if (existing.aggregationCoordinatorOrigin !== coordinatorOrigin) {
        throw new TypeError(
          'context_id is already bound to a different aggregation_coordinator_origin',
        );
      }
      if (existing.filteringId !== batchFilteringId) {
        throw new TypeError(
          'all contributions in one report must use the same filtering_id',
        );
      }
    }
    const state: ReportState =
      existing ??
      ({
        contextId: options.contextId,
        aggregationCoordinatorOrigin: coordinatorOrigin,
        filteringId: batchFilteringId,
        debugMode: false,
        contributions: [],
      } satisfies ReportState);

    // 6. 调试模式仅在第三方 Cookie 可用时生效；否则按未开启处理。
    if (options.debugMode === true && capabilities.thirdPartyCookiesEnabled) {
      state.debugMode = true;
    }

    // 7. 容量上限：按顺序接收，超出部分静默丢弃（不抛错）。
    const remaining = maxContributions - state.contributions.length;
    if (remaining > 0) {
      state.contributions.push(...accepted.slice(0, remaining));
    }
    if (existing === undefined) {
      reports.set(options.contextId, state);
    }
  }

  function drainReport(
    contextId: string,
    sink: ReportSink,
  ): ReportRecord | null {
    if (typeof contextId !== 'string' || contextId.length === 0) {
      throw new TypeError('context_id must be a non-empty string');
    }
    if (sink === null || typeof sink !== 'object' || typeof sink.send !== 'function') {
      throw new TypeError('sink must implement send(report)');
    }
    const state = reports.get(contextId);
    if (state === undefined) {
      return null;
    }

    const report: PreparedReport = {
      contextId: state.contextId,
      aggregationCoordinatorOrigin: state.aggregationCoordinatorOrigin,
      filteringId: state.filteringId,
      debugMode: state.debugMode,
      contributions: state.contributions.map((entry) =>
        Object.freeze({ ...entry }),
      ),
    };
    Object.freeze(report.contributions);
    Object.freeze(report);

    sink.send(report);
    reports.delete(contextId);

    return {
      contextId: report.contextId,
      aggregationCoordinatorOrigin: report.aggregationCoordinatorOrigin,
      filteringId: report.filteringId,
      debugMode: report.debugMode,
      contributionCount: report.contributions.length,
    };
  }

  const gate: ContributionGate = {
    contributeToHistogram,
  };
  Object.freeze(gate);

  return { gate, drainReport };
}
