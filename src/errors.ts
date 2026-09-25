import type { Path } from './types.ts';
import { pathToString } from './types.ts';

/** 所有树错误的基类：携带出错的具体路径。 */
export class JsonTreeError extends Error {
  readonly path: Path;
  constructor(message: string, path: Path) {
    super(`${message} (at ${pathToString(path)})`);
    this.name = new.target.name;
    this.path = path;
  }
}

/** 稳定 ID 模式下同一数组出现重复 id。 */
export class DuplicateArrayIdError extends JsonTreeError {
  readonly id: string;
  constructor(id: string, path: Path) {
    super(`duplicate array element id ${JSON.stringify(id)}`, path);
    this.id = id;
  }
}

/** 补丁某一步应用失败；此前已应用的步骤会被回滚。 */
export class PatchApplyError extends JsonTreeError {
  readonly opIndex: number;
  constructor(message: string, path: Path, opIndex: number) {
    super(message, path);
    this.opIndex = opIndex;
  }
}

/** 基线/目标摘要校验失败（先于任何修改抛出）。 */
export class DigestMismatchError extends JsonTreeError {
  readonly expected: string;
  readonly actual: string;
  constructor(kind: 'base' | 'target', expected: string, actual: string, path: Path) {
    super(`${kind} digest mismatch: expected ${expected}, got ${actual}`, path);
    this.expected = expected;
    this.actual = actual;
  }
}
