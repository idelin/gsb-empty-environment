import type { Path } from './path.ts';
import { canonical } from './path.ts';

/** 所有查询引擎错误的基类；能定位时携带规范路径。 */
export class QueryError extends Error {
  readonly code: string;
  readonly path?: string;
  constructor(code: string, message: string, path?: Path) {
    super(path ? `${message} (at ${canonical(path)})` : message);
    this.name = new.target.name;
    this.code = code;
    if (path) this.path = canonical(path);
  }
}

/** 查询串解析错误：定位到查询串中的字符位置。 */
export class ParseError extends QueryError {
  readonly position: number;
  readonly source: string;
  constructor(message: string, position: number, source: string) {
    super('PARSE_ERROR', `${message} (position ${position})`);
    this.position = position;
    this.source = source;
  }
}

export type BudgetPhase = 'parse' | 'expand' | 'compare';

/** 查询预算耗尽：阶段 + 触发时的规范路径。 */
export class BudgetExceededError extends QueryError {
  readonly phase: BudgetPhase;
  constructor(phase: BudgetPhase, path?: Path) {
    super('BUDGET_EXCEEDED', `query budget exhausted in phase "${phase}"`, path);
    this.phase = phase;
  }
}

/** 有界暂存溢出且未配置溢写目录。 */
export class BufferOverflowError extends QueryError {
  constructor(path: Path, limit: number) {
    super('BUFFER_OVERFLOW', `bounded staging exceeded limit ${limit}; configure spillDir to spill`, path);
  }
}

/** 通过 AbortSignal 取消；临时溢写数据已清理。 */
export class CancelledError extends QueryError {
  constructor() {
    super('CANCELLED', 'query cancelled; temporary data cleaned up');
  }
}

/** 文档编辑（增删改）错误：定位到编辑路径。 */
export class EditError extends QueryError {
  constructor(message: string, path: Path) {
    super('EDIT_ERROR', message, path);
  }
}
