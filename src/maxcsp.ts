/**
 * 带权最大约束满足问题（Weighted Max-CSP）分支定界求解器。
 *
 * 问题模型：
 *   - 一组变量，每个变量有有限值域；
 *   - 硬约束：必须全部满足，否则该分支直接剪除；
 *   - 软约束：每个软约束带有破坏惩罚权重，允许被违反，
 *     目标是最小化「被违反软约束的权重之和」。
 *
 * 算法：深度优先分支定界（Branch and Bound）。
 *   - 下界（lower bound）：当前部分赋值下「已确定被违反」的软约束权重之和。
 *     尚未判定的软约束按 0 计入，因此该下界是可采纳的（admissible），
 *     即任何完整扩展的真实代价都不会低于它。
 *   - 剪枝：当下界 >= 当前最优解代价时，剪掉该子树。
 *   - 值排序：对每个变量按「立即新增的软约束惩罚」升序尝试取值，
 *     以便尽早获得高质量 incumbent，从而提升剪枝效率。
 */

export type Value = number | string;
export type Assignment = Readonly<Record<string, Value>>;

export interface Variable {
  name: string;
  domain: readonly Value[];
}

/**
 * 硬约束。`consistent` 接收部分赋值：
 * 仅当约束所涉及的变量已全部赋值且约束被破坏时返回 false；
 * 涉及未赋值变量时返回 true（留待搜索深入后再判定）。
 */
export interface HardConstraint {
  name: string;
  description?: string;
  consistent(assignment: Assignment): boolean;
}

/**
 * 软约束。`violated` 接收部分赋值：
 *   - true  —— 已确定被违反（计入惩罚）；
 *   - false —— 已确定被满足；
 *   - null  —— 涉及未赋值变量，暂无法判定（按下界 0 处理）。
 */
export interface SoftConstraint {
  name: string;
  weight: number;
  description?: string;
  violated(assignment: Assignment): boolean | null;
}

export interface Violation {
  name: string;
  weight: number;
  description?: string;
}

export interface SolveStats {
  /** 展开过的搜索节点数（含根节点）。 */
  nodesExplored: number;
  /** 因下界不小于当前最优而被剪掉的子树次数。 */
  nodesPruned: number;
  /** 因违反硬约束而被剪掉的子树次数。 */
  hardPruned: number;
  /** 到达叶子（完整赋值）的次数。 */
  leavesVisited: number;
  /** 完整赋值方案的理论总数（各值域大小之积）。 */
  totalLeafSpace: number;
}

export interface Solution {
  assignment: Record<string, Value>;
  /** 总惩罚 = 被违反软约束权重之和。 */
  cost: number;
  violations: Violation[];
  stats: SolveStats;
}

export class MaxCSPSolver {
  private readonly variables: Variable[] = [];
  private readonly hardConstraints: HardConstraint[] = [];
  private readonly softConstraints: SoftConstraint[] = [];

  addVariable(name: string, domain: readonly Value[]): this {
    if (domain.length === 0) {
      throw new Error(`变量 ${name} 的值域不能为空`);
    }
    if (this.variables.some((v) => v.name === name)) {
      throw new Error(`变量 ${name} 重复定义`);
    }
    this.variables.push({ name, domain: [...domain] });
    return this;
  }

  addHardConstraint(constraint: HardConstraint): this {
    this.hardConstraints.push(constraint);
    return this;
  }

  addSoftConstraint(constraint: SoftConstraint): this {
    if (!(constraint.weight > 0)) {
      throw new Error(`软约束 ${constraint.name} 的权重必须为正数`);
    }
    this.softConstraints.push(constraint);
    return this;
  }

  /**
   * 分支定界求解。
   * @returns 全局最优解；若硬约束导致问题不可行，返回 null。
   */
  solve(): Solution | null {
    const stats: SolveStats = {
      nodesExplored: 0,
      nodesPruned: 0,
      hardPruned: 0,
      leavesVisited: 0,
      totalLeafSpace: this.variables.reduce((acc, v) => acc * v.domain.length, 1),
    };

    let best: { assignment: Record<string, Value>; cost: number } | null = null;
    const current: Record<string, Value> = {};

    const hardConsistent = (): boolean => {
      for (const hc of this.hardConstraints) {
        if (!hc.consistent(current)) return false;
      }
      return true;
    };

    /** 当前部分赋值下的可采纳下界：已确定违反的软约束权重之和。 */
    const lowerBound = (): number => {
      let bound = 0;
      for (const sc of this.softConstraints) {
        if (sc.violated(current) === true) bound += sc.weight;
      }
      return bound;
    };

    const search = (depth: number, bound: number): void => {
      stats.nodesExplored += 1;

      if (best !== null && bound >= best.cost) {
        stats.nodesPruned += 1;
        return;
      }

      if (depth === this.variables.length) {
        stats.leavesVisited += 1;
        if (best === null || bound < best.cost) {
          best = { assignment: { ...current }, cost: bound };
        }
        return;
      }

      const variable = this.variables[depth]!;
      // 值排序：优先尝试「立即新增惩罚」小的取值，尽早获得优质 incumbent。
      const orderedDomain = [...variable.domain].sort((x, y) => {
        current[variable.name] = x;
        const penaltyX = lowerBound();
        current[variable.name] = y;
        const penaltyY = lowerBound();
        delete current[variable.name];
        return penaltyX - penaltyY;
      });

      for (const value of orderedDomain) {
        current[variable.name] = value;
        if (!hardConsistent()) {
          stats.hardPruned += 1;
        } else {
          search(depth + 1, lowerBound());
        }
        delete current[variable.name];
      }
    };

    if (!hardConsistent()) {
      return null;
    }
    search(0, lowerBound());

    if (best === null) {
      return null;
    }

    const finalBest: { assignment: Record<string, Value>; cost: number } = best;
    const violations: Violation[] = this.softConstraints
      .filter((sc) => sc.violated(finalBest.assignment) === true)
      .map((sc) => ({ name: sc.name, weight: sc.weight, description: sc.description }));

    return { assignment: finalBest.assignment, cost: finalBest.cost, violations, stats };
  }
}
