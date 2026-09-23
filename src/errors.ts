/** 作用域已关闭（含关闭中）后仍尝试获取资源。 */
export class ScopeDestroyedError extends Error {
  constructor(message = "scope is destroyed or being destroyed") {
    super(message);
    this.name = "ScopeDestroyedError";
  }
}

/** 取消令牌被重复使用。 */
export class AlreadyCanceledError extends Error {
  constructor(message = "acquire already canceled") {
    super(message);
    this.name = "AlreadyCanceledError";
  }
}

/** 依赖图存在环；作为聚合错误中的确定性成员出现。 */
export class DependencyCycleError extends Error {
  readonly members: readonly string[];
  constructor(members: readonly string[]) {
    super(`dependency cycle among resources: ${members.join(" -> ")}`);
    this.name = "DependencyCycleError";
    this.members = members;
  }
}

/** 借用的资源不存在或尚未就绪。 */
export class BorrowError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "BorrowError";
  }
}

export function errorMessage(err: unknown): string {
  if (err instanceof Error) return `${err.name}: ${err.message}`;
  return String(err);
}
