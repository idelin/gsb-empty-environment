import type {
  ExistingTargetSecret,
  Finding,
  RefreshResult,
} from './types.ts';

export interface RefreshPlan {
  /** 是否应跳过本次写入。 */
  skip: boolean;
  findings: Finding[];
  /** 允许写入时规划出的数据；跳过时为空对象。 */
  plannedData: Record<string, string>;
}

/**
 * 刷新安全：
 * - 刷新失败：保留现有 Secret，绝不以空数据替换旧值；
 * - 成功但个别键取到空值且现有数据非空：剔除该键，避免旧值被降级为空。
 */
export function planRefresh(
  refresh: RefreshResult,
  existing: ExistingTargetSecret | undefined,
): RefreshPlan {
  if (refresh.failed) {
    return {
      skip: true,
      plannedData: {},
      findings: [
        {
          code: 'REFRESH_FAILED',
          message: '外部密钥库刷新失败，保留现有 Secret 数据，跳过本次写入',
        },
      ],
    };
  }

  const currentData = existing?.data ?? {};
  const plannedData: Record<string, string> = {};
  const downgradedKeys: string[] = [];

  for (const [key, value] of Object.entries(refresh.fetched)) {
    if (value === '' && currentData[key] !== undefined && currentData[key] !== '') {
      downgradedKeys.push(key);
      continue;
    }
    plannedData[key] = value;
  }

  const findings: Finding[] = [];
  if (downgradedKeys.length > 0) {
    findings.push({
      code: 'EMPTY_VALUE_DOWNGRADE',
      message: '刷新结果中的空值未替换现有非空数据，相关键已保留旧值',
      details: { keys: downgradedKeys },
    });
  }

  return { skip: false, findings, plannedData };
}
