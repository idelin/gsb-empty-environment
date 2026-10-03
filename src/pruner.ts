import type { Schema } from "./schema.ts";
import { fieldNames } from "./schema.ts";
import { Filter, Join, Operator, Project, Scan } from "./operators.ts";

/** 一次修剪运行的统计信息，用于量化内存 / I/O 收益。 */
export interface PruneReport {
  /** 每个被裁剪的 Scan：表名 -> 修剪前后的列名。 */
  scans: Array<{
    table: string;
    before: string[];
    after: string[];
  }>;
  /** 被整体消除的投影表达式（输出列无人消费）。 */
  eliminatedProjections: string[];
  /** 全树累计裁掉的列数。 */
  prunedColumnCount: number;
}

/**
 * 全树投影修剪器。
 *
 * 自顶向下传播“上层真正消费的字段集合”，逐层收紧：
 *  - Project：消除输出列无人消费的表达式（投影消除）；
 *  - Filter ：谓词引用字段强制保留，其余透传上层需求；
 *  - Join   ：上层需求按左右孩子输出模式拆分，再叠加各自 join key；
 *  - Scan   ：叶子节点，直接把输出模式压缩为被需要的列（无用字段修剪）。
 */
export class ProjectionPruner {
  /**
   * 以 `needed` 为顶层消费字段修剪整棵算子树（原地修改）。
   * 返回修剪报告。
   */
  prune(root: Operator, needed: ReadonlySet<string>): PruneReport {
    const report: PruneReport = {
      scans: [],
      eliminatedProjections: [],
      prunedColumnCount: 0,
    };
    this.pruneNode(root, new Set(needed), report);
    return report;
  }

  private pruneNode(op: Operator, needed: Set<string>, report: PruneReport): void {
    if (op instanceof Scan) {
      this.pruneScan(op, needed, report);
      return;
    }
    if (op instanceof Project) {
      this.pruneProject(op, needed, report);
      return;
    }
    if (op instanceof Filter) {
      // 过滤算子：上层需要的列 + 谓词自身引用的列，一起向下传播。
      const childNeeded = new Set(needed);
      for (const ref of op.predicateRefs) childNeeded.add(ref);
      this.pruneNode(op.children[0], childNeeded, report);
      op.refreshOutputSchema();
      return;
    }
    if (op instanceof Join) {
      this.pruneJoin(op, needed, report);
      return;
    }
    throw new Error(`Unsupported operator kind: ${op.kind}`);
  }

  private pruneScan(scan: Scan, needed: Set<string>, report: PruneReport): void {
    const before = fieldNames(scan.outputSchema);
    const after = scan.outputSchema.filter((f) => needed.has(f.name));
    if (after.length === 0) {
      throw new Error(
        `Pruning would leave Scan(${scan.tableName}) with zero columns; ` +
          `at least one column must be consumed upstream.`,
      );
    }
    scan.outputSchema = after;
    report.scans.push({ table: scan.tableName, before, after: fieldNames(after) });
    report.prunedColumnCount += before.length - after.length;
  }

  private pruneProject(project: Project, needed: Set<string>, report: PruneReport): void {
    // 投影消除：丢弃输出列不被上层消费的表达式。
    const kept = project.expressions.filter((expr) => needed.has(expr.name));
    const eliminated = project.expressions.filter((expr) => !needed.has(expr.name));
    for (const expr of eliminated) report.eliminatedProjections.push(expr.name);
    if (kept.length === 0) {
      throw new Error("Pruning would eliminate every projection expression.");
    }
    project.expressions = kept;

    // 下层只需提供被保留表达式引用到的字段。
    const childNeeded = new Set<string>();
    for (const expr of kept) {
      for (const ref of expr.refs) childNeeded.add(ref);
    }
    this.pruneNode(project.children[0], childNeeded, report);
    project.refreshOutputSchema();
  }

  private pruneJoin(join: Join, needed: Set<string>, report: PruneReport): void {
    join.children.forEach((child, index) => {
      const childOutputs = new Set(fieldNames(child.outputSchema));
      // 上层需求中由该侧提供的列 + 该侧 join key。
      const childNeeded = new Set<string>();
      for (const name of needed) {
        if (childOutputs.has(name)) childNeeded.add(name);
      }
      for (const key of join.localRequiredFields(index)) childNeeded.add(key);
      this.pruneNode(child, childNeeded, report);
    });
    join.refreshOutputSchema();
  }
}

/** 便捷函数：收集整棵树当前的输出模式快照（用于日志 / 断言）。 */
export function snapshotSchemas(root: Operator): Array<{ kind: string; schema: Schema }> {
  const out: Array<{ kind: string; schema: Schema }> = [
    { kind: root.kind, schema: root.outputSchema },
  ];
  for (const child of root.children) out.push(...snapshotSchemas(child));
  return out;
}
