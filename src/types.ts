/** 单元格取值；空值一律用 null 表示，独立处理、不参与层级提升。 */
export type CellValue = string | number | null;

/**
 * 某列的有限泛化层级。
 * levels[i] 是一张映射表：把「第 i 层的取值」提升为「第 i+1 层的取值」。
 * 因此 levels[0] 作用于原始值，levels.length 即该列允许的最高层级。
 * levels 为空数组表示该列不允许泛化（只能停在第 0 层）。
 */
export interface Hierarchy {
  levels: Array<Record<string, string>>;
}

export interface Dataset {
  columns: string[];
  hierarchies: Record<string, Hierarchy>;
  rows: Array<Record<string, CellValue>>;
}

export interface GroupInfo {
  /** 该等价组在各列上的泛化后取值（空值保持 null）。 */
  values: Record<string, CellValue>;
  count: number;
}

export interface Solution {
  feasible: true;
  k: number;
  /** 每列统一采用的层级（所有行一致，不逐行变化）。 */
  levels: Record<string, number>;
  groups: GroupInfo[];
  groupCount: number;
  minGroupSize: number;
  /** 总泛化损失：所有行的逐行损失之和，逐行损失 = Σ列 level_c / maxLevel_c。 */
  totalLoss: number;
  /** 归一化平均损失：totalLoss / (行数 × 列数)，取值 [0, 1]。 */
  averageLoss: number;
}

export interface NoSolution {
  feasible: false;
  reason: 'INSUFFICIENT_RECORDS' | 'NO_FEASIBLE_SCHEME';
  message: string;
  /** 无可行方案时，最高层级下仍违反 k 约束的分组示例。 */
  violatingGroups?: GroupInfo[];
}

export type Result = Solution | NoSolution;
