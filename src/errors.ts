/** 生命周期库的所有错误类型，均继承自 ModuleLifecycleError。 */

export class ModuleLifecycleError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

/** 装载一个尚未 define 的模块时抛出。 */
export class ModuleNotDefinedError extends ModuleLifecycleError {
  readonly moduleName: string;

  constructor(moduleName: string) {
    super(`模块 "${moduleName}" 尚未定义，无法装载`);
    this.moduleName = moduleName;
  }
}

/** require 链上出现环时抛出，cyclePath 给出可读的循环路径（如 ["a", "b", "a"]）。 */
export class DependencyCycleError extends ModuleLifecycleError {
  readonly cyclePath: readonly string[];

  constructor(cyclePath: readonly string[]) {
    super(`检测到依赖循环：${cyclePath.join(" -> ")}`);
    this.cyclePath = [...cyclePath];
  }
}

/** 工厂函数执行失败时抛出，cause 保留原始异常；已登记的依赖会被逆序回滚。 */
export class ModuleLoadError extends ModuleLifecycleError {
  readonly moduleName: string;
  override readonly cause: unknown;

  constructor(moduleName: string, cause: unknown) {
    super(
      `模块 "${moduleName}" 装载失败：${cause instanceof Error ? cause.message : String(cause)}`,
    );
    this.moduleName = moduleName;
    this.cause = cause;
  }
}
