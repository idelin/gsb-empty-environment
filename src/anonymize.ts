import type {
  CellValue,
  Dataset,
  GroupInfo,
  Hierarchy,
  Result,
} from './types.ts';

/** 把取值沿层级链提升 level 步；空值保持 null，独立成类。 */
function generalizeValue(
  value: CellValue,
  hierarchy: Hierarchy,
  level: number,
): CellValue {
  if (value === null || value === undefined) return null;
  let current = String(value);
  for (let step = 0; step < level; step += 1) {
    current = hierarchy.levels[step][current];
  }
  return current;
}

/** 校验数据集与层级定义，发现问题直接抛出带中文说明的错误。 */
export function validateDataset(dataset: Dataset): void {
  if (!Array.isArray(dataset.columns) || dataset.columns.length === 0) {
    throw new Error('数据集必须至少包含一列。');
  }
  for (const column of dataset.columns) {
    const hierarchy = dataset.hierarchies[column];
    if (!hierarchy || !Array.isArray(hierarchy.levels)) {
      throw new Error(`列 "${column}" 缺少层级定义 hierarchies["${column}"].levels。`);
    }
    const distinct = new Set<string>();
    for (const row of dataset.rows) {
      const value = row[column];
      if (value !== null && value !== undefined) distinct.add(String(value));
    }
    for (const raw of distinct) {
      let current = raw;
      for (let step = 0; step < hierarchy.levels.length; step += 1) {
        const table = hierarchy.levels[step];
        if (!(current in table)) {
          throw new Error(
            `列 "${column}" 的值 "${current}" 在第 ${step + 1} 层映射表中缺失，无法提升。`,
          );
        }
        current = table[current];
      }
    }
  }
}

interface LevelVector {
  levels: number[];
  /** 单行损失 = Σ列 level_c / maxLevel_c（maxLevel 为 0 的列贡献 0）。 */
  perRowLoss: number;
}

/** 枚举全部统一列级方案，并按单行损失升序排列。 */
function enumerateLevelVectors(maxLevels: number[]): LevelVector[] {
  const vectors: LevelVector[] = [];
  const current: number[] = new Array(maxLevels.length).fill(0);
  const perRowLossOf = (levels: number[]): number =>
    levels.reduce(
      (sum, level, index) =>
        sum + (maxLevels[index] === 0 ? 0 : level / maxLevels[index]),
      0,
    );
  const walk = (columnIndex: number): void => {
    if (columnIndex === maxLevels.length) {
      vectors.push({ levels: [...current], perRowLoss: perRowLossOf(current) });
      return;
    }
    for (let level = 0; level <= maxLevels[columnIndex]; level += 1) {
      current[columnIndex] = level;
      walk(columnIndex + 1);
    }
  };
  walk(0);
  vectors.sort((a, b) => a.perRowLoss - b.perRowLoss);
  return vectors;
}

/** 按给定统一层级对全表分组，返回各等价组及其计数。 */
function groupRows(
  dataset: Dataset,
  levelVector: number[],
): GroupInfo[] {
  const groups = new Map<string, GroupInfo>();
  for (const row of dataset.rows) {
    const values: Record<string, CellValue> = {};
    const keyParts: CellValue[] = [];
    dataset.columns.forEach((column, index) => {
      const generalized = generalizeValue(
        row[column],
        dataset.hierarchies[column],
        levelVector[index],
      );
      values[column] = generalized;
      keyParts.push(generalized);
    });
    const key = JSON.stringify(keyParts);
    const existing = groups.get(key);
    if (existing) {
      existing.count += 1;
    } else {
      groups.set(key, { values, count: 1 });
    }
  }
  return [...groups.values()];
}

/**
 * 求解总泛化损失最小的统一列级 k-匿名方案。
 *
 * - 所有行在同一列上使用同一层级（不做逐行/逐单元格泛化）；
 * - 空值独立处理：null 不随层级提升，只与其他 null 同组；
 * - 约束：每个最终等价组合的人数 >= k；
 * - 目标：最小化总损失 = 行数 × Σ列 level_c / maxLevel_c；
 * - 总人数不足 k，或最高层级下仍有小组时，明确返回无解。
 */
export function findOptimalScheme(dataset: Dataset, k = 3): Result {
  validateDataset(dataset);
  if (!Number.isInteger(k) || k < 2) {
    throw new Error(`k 必须是不小于 2 的整数，收到：${k}`);
  }

  const rowCount = dataset.rows.length;
  if (rowCount < k) {
    return {
      feasible: false,
      reason: 'INSUFFICIENT_RECORDS',
      message: `无解：总人数 ${rowCount} 小于 k=${k}，任何分组都无法满足每组至少 ${k} 人。`,
    };
  }

  const maxLevels = dataset.columns.map(
    (column) => dataset.hierarchies[column].levels.length,
  );
  const candidates = enumerateLevelVectors(maxLevels);

  for (const candidate of candidates) {
    const groups = groupRows(dataset, candidate.levels);
    const minGroupSize = Math.min(...groups.map((group) => group.count));
    if (minGroupSize >= k) {
      const levels: Record<string, number> = {};
      dataset.columns.forEach((column, index) => {
        levels[column] = candidate.levels[index];
      });
      const totalLoss = candidate.perRowLoss * rowCount;
      return {
        feasible: true,
        k,
        levels,
        groups: groups.sort((a, b) => b.count - a.count),
        groupCount: groups.length,
        minGroupSize,
        totalLoss,
        averageLoss: totalLoss / (rowCount * dataset.columns.length),
      };
    }
  }

  const topGroups = groupRows(dataset, maxLevels);
  const violatingGroups = topGroups
    .filter((group) => group.count < k)
    .sort((a, b) => a.count - b.count);
  return {
    feasible: false,
    reason: 'NO_FEASIBLE_SCHEME',
    message:
      `无解：即使所有列都提升到最高层级，仍存在 ${violatingGroups.length} 个` +
      `人数不足 ${k} 的组合（最小组仅 ${violatingGroups[0]?.count ?? 0} 人）。`,
    violatingGroups,
  };
}
