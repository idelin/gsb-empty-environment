/**
 * 模块生命周期库
 *
 * 核心语义：
 * - 允许反复装载同名模块，每次装载返回独立的释放函数（句柄）。
 * - 句柄绑定到具体的模块记录（世代），旧句柄的释放函数晚调用时
 *   不会卸载新世代模块；同一句柄重复释放无副作用。
 * - 依赖在装载时递归装载并登记引用，卸载时按装载逆序清理。
 * - 装载中途失败会回滚已登记的依赖；依赖循环会被检测并报出路径。
 * - 全生命周期产生可解释的结构化日志。
 */

export type LifecycleEventType =
  | "define"
  | "load:start"
  | "load:success"
  | "load:failure"
  | "ref:acquire"
  | "ref:release"
  | "unload:start"
  | "unload:success"
  | "unload:failure"
  | "rollback"
  | "release:ignored"
  | "shutdown:start"
  | "shutdown:success";

export interface LifecycleEvent {
  /** 全局递增序号，保证日志顺序可解释 */
  readonly seq: number;
  readonly type: LifecycleEventType;
  readonly module: string;
  /** 面向人类的中文描述 */
  readonly message: string;
  readonly details?: Record<string, unknown>;
}

export interface SetupContext {
  /** 正在装载的模块名 */
  readonly name: string;
  /** 该模块名的世代号（首次装载为 1，卸载后再次装载递增） */
  readonly generation: number;
}

export interface ModuleDefinition<T = unknown> {
  readonly name: string;
  /** 依赖的模块名，装载时按声明顺序递归装载 */
  readonly dependencies?: readonly string[];
  /** 构造模块实例；缺省时实例为 undefined */
  readonly setup?: (ctx: SetupContext) => T;
  /** 卸载钩子；抛错不会中断其余清理，只会记录 unload:failure 日志 */
  readonly teardown?: (instance: T) => void;
}

/** 一次装载得到的句柄。release 幂等，且只影响其绑定的那个世代。 */
export interface ModuleHandle<T = unknown> {
  readonly name: string;
  readonly generation: number;
  readonly instance: T;
  /** 该句柄是否已被释放 */
  readonly released: boolean;
  release(): void;
}

export class ModuleNotDefinedError extends Error {
  constructor(readonly moduleName: string) {
    super(`模块未定义: "${moduleName}"`);
    this.name = "ModuleNotDefinedError";
  }
}

export class ModuleAlreadyDefinedError extends Error {
  constructor(readonly moduleName: string) {
    super(`模块重复定义: "${moduleName}"`);
    this.name = "ModuleAlreadyDefinedError";
  }
}

export class DependencyCycleError extends Error {
  constructor(readonly path: readonly string[]) {
    super(`检测到依赖循环: ${path.join(" -> ")}`);
    this.name = "DependencyCycleError";
  }
}

export class ModuleLoadError extends Error {
  constructor(
    readonly moduleName: string,
    readonly cause: unknown,
  ) {
    super(
      `装载模块 "${moduleName}" 失败: ${
        cause instanceof Error ? cause.message : String(cause)
      }`,
    );
    this.name = "ModuleLoadError";
  }
}

interface DependencyLease {
  readonly name: string;
  readonly release: () => void;
}

/** 一次成功装载在注册表中的记录，即模块的一个“世代”。 */
interface ModuleRecord {
  readonly name: string;
  readonly definition: ModuleDefinition;
  readonly instance: unknown;
  readonly generation: number;
  /** 存活的引用数（句柄数） */
  refs: number;
  alive: boolean;
  /** 装载期间登记的依赖租约，卸载时逆序释放 */
  readonly dependencyLeases: DependencyLease[];
}

class ModuleHandleImpl<T> implements ModuleHandle<T> {
  private active = true;

  constructor(
    private readonly kernel: ModuleLifecycle,
    private readonly record: ModuleRecord,
  ) {}

  get name(): string {
    return this.record.name;
  }

  get generation(): number {
    return this.record.generation;
  }

  get instance(): T {
    return this.record.instance as T;
  }

  get released(): boolean {
    return !this.active;
  }

  release(): void {
    if (!this.active) {
      this.kernel.emit("release:ignored", this.record.name, `句柄被重复释放，已忽略`, {
        generation: this.record.generation,
        reason: "duplicate-release",
      });
      return;
    }
    this.active = false;
    this.kernel.releaseRecord(this.record);
  }
}

export class ModuleLifecycle {
  private readonly definitions = new Map<string, ModuleDefinition>();
  private readonly records = new Map<string, ModuleRecord>();
  /** 存活记录按装载先后排列，shutdown 时逆序卸载 */
  private readonly loadOrder: ModuleRecord[] = [];
  /** 当前装载调用栈，用于依赖循环检测（装载过程是同步的） */
  private readonly loadStack: string[] = [];
  private readonly generations = new Map<string, number>();
  private seq = 0;
  readonly events: LifecycleEvent[] = [];

  /** 注册模块定义。同名重复定义会抛错。 */
  define<T>(definition: ModuleDefinition<T>): this {
    if (this.definitions.has(definition.name)) {
      throw new ModuleAlreadyDefinedError(definition.name);
    }
    this.definitions.set(definition.name, definition as ModuleDefinition);
    this.emit("define", definition.name, `登记模块定义，依赖: [${(definition.dependencies ?? []).join(", ")}]`, {
      dependencies: [...(definition.dependencies ?? [])],
    });
    return this;
  }

  /**
   * 装载模块，返回句柄。
   * 同名模块已存活时仅递增引用并返回新句柄；否则完整执行
   * 依赖装载 -> setup -> 登记记录 的流程。
   */
  load<T = unknown>(name: string): ModuleHandle<T> {
    const definition = this.definitions.get(name);
    if (!definition) {
      throw new ModuleNotDefinedError(name);
    }

    const cycleAt = this.loadStack.indexOf(name);
    if (cycleAt !== -1) {
      const path = [...this.loadStack.slice(cycleAt), name];
      this.emit("load:failure", name, `检测到依赖循环: ${path.join(" -> ")}`, { path });
      throw new DependencyCycleError(path);
    }

    const existing = this.records.get(name);
    if (existing && existing.alive) {
      existing.refs += 1;
      this.emit("ref:acquire", name, `复用存活实例，引用计数增至 ${existing.refs}`, {
        generation: existing.generation,
        refs: existing.refs,
      });
      return new ModuleHandleImpl<T>(this, existing);
    }

    const generation = (this.generations.get(name) ?? 0) + 1;
    this.generations.set(name, generation);
    const dependencies = [...(definition.dependencies ?? [])];
    this.emit("load:start", name, `开始装载（世代 ${generation}），依赖: [${dependencies.join(", ")}]`, {
      generation,
      dependencies,
    });

    this.loadStack.push(name);
    const acquired: DependencyLease[] = [];
    try {
      for (const dep of dependencies) {
        const handle = this.load(dep);
        acquired.push({ name: dep, release: () => handle.release() });
        this.emit("ref:acquire", name, `登记对依赖 "${dep}" 的引用`, {
          generation,
          dependency: dep,
        });
      }

      const instance = definition.setup
        ? definition.setup({ name, generation })
        : undefined;

      const record: ModuleRecord = {
        name,
        definition,
        instance,
        generation,
        refs: 1,
        alive: true,
        dependencyLeases: acquired,
      };
      this.records.set(name, record);
      this.loadOrder.push(record);
      this.emit("load:success", name, `装载完成（世代 ${generation}），引用计数 1`, {
        generation,
        refs: 1,
      });
      return new ModuleHandleImpl<T>(this, record);
    } catch (error) {
      this.emit("load:failure", name, `装载失败，开始回滚已登记的 ${acquired.length} 个依赖`, {
        generation,
        error: error instanceof Error ? error.message : String(error),
      });
      // 逆序回滚本层已登记的依赖
      for (const lease of [...acquired].reverse()) {
        try {
          lease.release();
          this.emit("rollback", name, `已回滚对依赖 "${lease.name}" 的引用`, {
            generation,
            dependency: lease.name,
          });
        } catch (rollbackError) {
          this.emit("rollback", name, `回滚依赖 "${lease.name}" 时出错（已忽略，继续回滚）`, {
            generation,
            dependency: lease.name,
            error: rollbackError instanceof Error ? rollbackError.message : String(rollbackError),
          });
        }
      }
      if (error instanceof DependencyCycleError || error instanceof ModuleLoadError) {
        throw error;
      }
      throw new ModuleLoadError(name, error);
    } finally {
      this.loadStack.pop();
    }
  }

  /** 由句柄触发：释放某条具体记录的一次引用。 */
  releaseRecord(record: ModuleRecord): void {
    // 记录可能已被卸载并被新世代取代——旧句柄绝不能影响新记录
    if (this.records.get(record.name) !== record || !record.alive) {
      this.emit("release:ignored", record.name, `句柄指向的记录已卸载（世代 ${record.generation}），释放被忽略`, {
        generation: record.generation,
        reason: "stale-record",
      });
      return;
    }
    record.refs -= 1;
    this.emit("ref:release", record.name, `释放一次引用，剩余引用计数 ${record.refs}`, {
      generation: record.generation,
      refs: record.refs,
    });
    if (record.refs > 0) {
      return;
    }
    this.unloadRecord(record);
  }

  /** 引用归零后的卸载：teardown -> 逆序释放依赖。 */
  private unloadRecord(record: ModuleRecord): void {
    this.emit("unload:start", record.name, `引用归零，开始卸载（世代 ${record.generation}）`, {
      generation: record.generation,
    });
    record.alive = false;
    this.records.delete(record.name);
    const index = this.loadOrder.indexOf(record);
    if (index !== -1) {
      this.loadOrder.splice(index, 1);
    }

    const teardown = record.definition.teardown;
    if (teardown) {
      try {
        teardown(record.instance);
      } catch (error) {
        this.emit("unload:failure", record.name, `teardown 抛错（已忽略，继续清理依赖）`, {
          generation: record.generation,
          error: error instanceof Error ? error.message : String(error),
        });
      }
    }

    // 逆序清理：后登记的依赖先释放
    for (const lease of [...record.dependencyLeases].reverse()) {
      lease.release();
    }

    this.emit("unload:success", record.name, `卸载完成（世代 ${record.generation}）`, {
      generation: record.generation,
    });
  }

  /** 逆装载顺序卸载全部存活模块；之后所有旧句柄的 release 均为无副作用操作。 */
  shutdown(): void {
    this.emit("shutdown:start", "*", `开始全局卸载，存活模块 ${this.loadOrder.length} 个`, {
      loaded: this.loadOrder.map((r) => r.name),
    });
    for (const record of [...this.loadOrder].reverse()) {
      if (record.alive) {
        this.unloadRecord(record);
      }
    }
    this.emit("shutdown:success", "*", `全局卸载完成`, {});
  }

  isLoaded(name: string): boolean {
    return this.records.has(name);
  }

  refCount(name: string): number {
    return this.records.get(name)?.refs ?? 0;
  }

  /** 当前存活模块名，按装载顺序。 */
  loadedModules(): string[] {
    return this.loadOrder.map((r) => r.name);
  }

  emit(type: LifecycleEventType, module: string, message: string, details?: Record<string, unknown>): void {
    this.events.push({ seq: ++this.seq, type, module, message, details });
  }

  /** 把生命周期日志渲染为可读的文本行。 */
  formatLog(): string {
    return this.events
      .map((e) => `#${String(e.seq).padStart(3, "0")} [${e.type}] ${e.module} — ${e.message}`)
      .join("\n");
  }
}
