import { Operator } from './operators.ts';

/**
 * 列投影消除与无用字段修剪器（Projection Pruning）。
 *
 * 自顶向下遍历算子树：根算子的需求即其全部输出列，
 * 每个算子把"上层真正消费的字段"向下传播，
 * 最终叶子 Scan 的输出模式被压缩为仅含被消费的列。
 */
export class ProjectionPruner {
  /**
   * 对整棵计划树执行修剪（原地修改）。
   * @param root 根算子
   * @param topLevelRequired 顶层消费者需要的字段；缺省为根算子的全部输出列
   */
  static prune(root: Operator, topLevelRequired?: Iterable<string>): Operator {
    const required = topLevelRequired
      ? new Set(topLevelRequired)
      : new Set(root.outputSchema());
    root.applyRequiredColumns(required);
    return root;
  }
}
