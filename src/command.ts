/**
 * 命令：执行 + 逆操作 + 前置版本。
 * execute/undo 直接修改传入的草稿状态（事务内的私有副本）。
 * expectedVersion 存在时，仅当提交基于该版本才允许执行。
 */
export interface Command<S> {
  readonly name: string;
  readonly expectedVersion?: number;
  execute(state: S): void;
  undo(state: S): void;
}
