import {
  DependencyCycleError,
  ModuleLifecycleError,
  ModuleLoadError,
  ModuleNotDefinedError,
} from "./errors.ts";
import type { LifecycleEvent, LifecycleLogger } from "./logger.ts";
import { ConsoleLogger } from "./logger.ts";

export type DisposeFn = () => void;

/** 工厂函数的执行上下文：登记依赖与清理回调。 */
export interface LoadContext {
  readonly moduleName: string;
  /** 装载依赖模块并登记到当前模块，当前模块卸载时会按逆序释放。 */
  require<T = unknown>(name: string): T;
  /** 登记当前模块的清理回调，卸载时按登记逆序执行。 */
  onDispose(fn: DisposeFn): void;
}

export type ModuleFactory<T> = (ctx: LoadContext) => T;

export interface DefineOptions<T> {
  /** 模块级清理回调，在卸载时与 ctx.onDispose 登记的回调一起按逆序执行。 */
  dispose?: (instance: T) => void;
}

/**
 * 释放函数：每次 load 返回一个，且只能生效一次。
 * 重复调用或模块已卸载后的迟调用都是安全的无操作。
 */
export interface Release<T> {
  (): void;
  readonly instance: T;
  readonly moduleName: string;
  readonly generation: number;
  /** 该释放函数是否已被消费。 */
  readonly released: boolean;
}

interface ModuleRecord {
  readonly name: string;
  readonly generation: number;
  instance: unknown;
  refCount: number;
  /** 依赖的释放函数，按登记顺序排列，清理时逆序消费。 */
  readonly dependencies: Release<unknown>[];
  readonly disposers: DisposeFn[];
  tornDown: boolean;
}

interface ModuleDefinition {
  readonly factory: ModuleFactory<unknown>;
  readonly dispose?: (instance: unknown) => void;
}

export interface ModuleLoaderOptions {
  logger?: LifecycleLogger;
}

/**
 * 模块生命周期装载器。
 *
 * 核心语义：
 * - 同名模块可反复装载；实例存活期间共享，引用计数归零才卸载。
 * - 每次 load 返回一个绑定到具体实例代次的释放函数，一次性、幂等。
 * - 旧代次的释放函数晚调用不会影响新代次实例。
 * - 依赖通过 ctx.require 登记，卸载与失败回滚都按逆序清理。
 * - 依赖循环在装载时检测并整体回滚。
 */
export class ModuleLoader {
  private readonly definitions = new Map<string, ModuleDefinition>();
  private readonly active = new Map<string, ModuleRecord>();
  private readonly loadingStack: string[] = [];
  private readonly loadingRecords: ModuleRecord[] = [];
  private readonly generations = new Map<string, number>();
  private readonly logger: LifecycleLogger;

  constructor(options: ModuleLoaderOptions = {}) {
    this.logger = options.logger ?? new ConsoleLogger();
  }

  define<T>(name: string, factory: ModuleFactory<T>, options: DefineOptions<T> = {}): this {
    if (this.definitions.has(name)) {
      throw new ModuleLifecycleError(`模块 "${name}" 已定义，不能重复定义`);
    }
    this.definitions.set(name, {
      factory: factory as ModuleFactory<unknown>,
      dispose: options.dispose as ((instance: unknown) => void) | undefined,
    });
    this.emit({ type: "defined", module: name, message: `注册模块定义 "${name}"` });
    return this;
  }

  isLoaded(name: string): boolean {
    return this.active.has(name);
  }

  refCount(name: string): number {
    return this.active.get(name)?.refCount ?? 0;
  }

  load<T = unknown>(name: string): Release<T> {
    const existing = this.active.get(name);
    if (existing) {
      existing.refCount += 1;
      this.emit({
        type: "attach",
        module: name,
        generation: existing.generation,
        message: `模块 "${name}" 第 ${existing.generation} 代实例仍在存活，本次装载复用它，引用计数增至 ${existing.refCount}`,
      });
      return this.makeRelease<T>(existing);
    }

    const definition = this.definitions.get(name);
    if (!definition) {
      throw new ModuleNotDefinedError(name);
    }

    const cycleAt = this.loadingStack.indexOf(name);
    if (cycleAt !== -1) {
      const cyclePath = [...this.loadingStack.slice(cycleAt), name];
      this.emit({
        type: "cycle-detected",
        module: name,
        message: `检测到依赖循环：${cyclePath.join(" -> ")}，中止装载并逆序回滚已登记的依赖`,
        detail: { cyclePath },
      });
      throw new DependencyCycleError(cyclePath);
    }

    const generation = (this.generations.get(name) ?? 0) + 1;
    this.generations.set(name, generation);
    const record: ModuleRecord = {
      name,
      generation,
      instance: undefined,
      refCount: 1,
      dependencies: [],
      disposers: [],
      tornDown: false,
    };

    this.emit({
      type: "load-start",
      module: name,
      generation,
      message: `开始装载模块 "${name}"（第 ${generation} 代实例）`,
    });

    this.loadingStack.push(name);
    this.loadingRecords.push(record);
    try {
      const instance = definition.factory(this.makeContext(record));
      record.instance = instance;
      if (definition.dispose) {
        record.disposers.push(() => definition.dispose!(instance));
      }
    } catch (error) {
      this.emit({
        type: "load-failure",
        module: name,
        generation,
        message: `模块 "${name}" 第 ${generation} 代装载失败：${describe(error)}，已登记的 ${record.dependencies.length} 个依赖将按逆序回滚`,
        detail: { error },
      });
      this.rollback(record);
      if (error instanceof ModuleLoadError || error instanceof DependencyCycleError) {
        throw error;
      }
      throw new ModuleLoadError(name, error);
    } finally {
      this.loadingStack.pop();
      this.loadingRecords.pop();
    }

    this.active.set(name, record);
    this.emit({
      type: "load-success",
      module: name,
      generation,
      message: `模块 "${name}" 第 ${generation} 代装载完成，引用计数 1，登记依赖 ${record.dependencies.length} 个`,
    });
    return this.makeRelease<T>(record);
  }

  private makeContext(record: ModuleRecord): LoadContext {
    return {
      moduleName: record.name,
      require: <T>(depName: string): T => {
        const depRelease = this.load<T>(depName);
        record.dependencies.push(depRelease as Release<unknown>);
        this.emit({
          type: "dependency-registered",
          module: record.name,
          generation: record.generation,
          message: `模块 "${record.name}"（第 ${record.generation} 代）登记第 ${record.dependencies.length} 个依赖 "${depName}"（第 ${depRelease.generation} 代），清理时将按逆序释放`,
          detail: {
            dependency: depName,
            dependencyGeneration: depRelease.generation,
            order: record.dependencies.length,
          },
        });
        return depRelease.instance;
      },
      onDispose: (fn: DisposeFn): void => {
        record.disposers.push(fn);
      },
    };
  }

  private makeRelease<T>(record: ModuleRecord): Release<T> {
    let used = false;
    const release = (() => {
      if (used) {
        this.emit({
          type: "release-ignored",
          module: record.name,
          generation: record.generation,
          message: `模块 "${record.name}" 第 ${record.generation} 代的该释放函数已被消费，重复释放无副作用`,
        });
        return;
      }
      used = true;
      if (record.tornDown) {
        this.emit({
          type: "release-ignored",
          module: record.name,
          generation: record.generation,
          message: `模块 "${record.name}" 第 ${record.generation} 代实例已卸载，该旧释放函数的迟调用不会影响当前实例`,
        });
        return;
      }
      record.refCount -= 1;
      this.emit({
        type: "release",
        module: record.name,
        generation: record.generation,
        message: `释放模块 "${record.name}" 第 ${record.generation} 代的一个引用，剩余引用 ${record.refCount}`,
      });
      if (record.refCount === 0) {
        this.teardown(record);
      }
    }) as Release<T>;
    Object.defineProperties(release, {
      instance: { get: () => record.instance as T },
      moduleName: { value: record.name },
      generation: { value: record.generation },
      released: { get: () => used },
    });
    return release;
  }

  /** 正常卸载：先逆序执行清理回调，再逆序释放依赖。 */
  private teardown(record: ModuleRecord): void {
    record.tornDown = true;
    this.active.delete(record.name);
    this.emit({
      type: "unload-start",
      module: record.name,
      generation: record.generation,
      message: `模块 "${record.name}" 第 ${record.generation} 代引用归零，开始卸载：先逆序执行 ${record.disposers.length} 个清理回调，再逆序释放 ${record.dependencies.length} 个依赖`,
    });
    this.runDisposers(record);
    this.releaseDependencies(record);
    this.emit({
      type: "unloaded",
      module: record.name,
      generation: record.generation,
      message: `模块 "${record.name}" 第 ${record.generation} 代卸载完成`,
    });
  }

  /** 装载失败回滚：与正常卸载相同的逆序清理，但实例未进入存活表。 */
  private rollback(record: ModuleRecord): void {
    record.tornDown = true;
    this.runDisposers(record);
    this.releaseDependencies(record);
    this.emit({
      type: "rollback",
      module: record.name,
      generation: record.generation,
      message: `模块 "${record.name}" 第 ${record.generation} 代回滚完成，装载现场已清理`,
    });
  }

  private runDisposers(record: ModuleRecord): void {
    for (const fn of [...record.disposers].reverse()) {
      try {
        fn();
      } catch (error) {
        this.emit({
          type: "dispose-error",
          module: record.name,
          generation: record.generation,
          message: `模块 "${record.name}" 第 ${record.generation} 代的清理回调执行失败：${describe(error)}，继续其余清理`,
          detail: { error },
        });
      }
    }
  }

  private releaseDependencies(record: ModuleRecord): void {
    for (const dep of [...record.dependencies].reverse()) {
      this.emit({
        type: "dependency-release",
        module: record.name,
        generation: record.generation,
        message: `模块 "${record.name}"（第 ${record.generation} 代）按逆序释放依赖 "${dep.moduleName}"（第 ${dep.generation} 代）`,
        detail: { dependency: dep.moduleName, dependencyGeneration: dep.generation },
      });
      dep();
    }
  }

  private emit(event: Omit<LifecycleEvent, "timestamp">): void {
    try {
      this.logger.log({ ...event, timestamp: Date.now() });
    } catch (error) {
      console.error(`[lifecycle] 日志器自身出错，已忽略：${describe(error)}`);
    }
  }
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
