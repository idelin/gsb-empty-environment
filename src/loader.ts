import { Planner } from "./planner.ts";
import type { Plan } from "./planner.ts";
import type { PluginContext, PluginModule, Registry } from "./types.ts";

export type PluginState = "planned" | "initializing" | "active" | "failed" | "unloaded";

interface PluginRecord {
  module: PluginModule;
  state: PluginState;
  instance?: unknown;
  failure?: unknown;
}

export class PluginLoader {
  readonly plan: Plan;
  private readonly registry?: Registry;
  private readonly records = new Map<string, PluginRecord>();
  readonly initCounts = new Map<string, number>();
  readonly disposeCounts = new Map<string, number>();

  constructor(plan: Plan, registry?: Registry) {
    this.plan = plan;
    this.registry = registry;
    for (const [name, module] of plan.selected) {
      this.records.set(name, { module, state: "planned" });
    }
  }

  getState(name: string): PluginState | undefined {
    return this.records.get(name)?.state;
  }

  getInstance(name: string): unknown {
    const record = this.records.get(name);
    return record && record.state === "active" ? record.instance : undefined;
  }

  getFailure(name: string): unknown {
    return this.records.get(name)?.failure;
  }

  initializeAll(): void {
    for (let index = 0; index < this.plan.order.length; index += 1) {
      const name = this.plan.order[index];
      const record = this.records.get(name);
      if (!record || record.state !== "planned") continue;
      try {
        this.initialize(name);
      } catch {
        // 失败已记录在案，继续初始化其余可独立工作的插件
      }
    }
  }

  initialize(name: string): unknown {
    const record = this.records.get(name);
    if (!record) throw new Error(`未知插件: ${name}`);
    if (record.state === "active") return record.instance;
    if (record.state === "initializing") {
      throw new Error(`插件 ${name} 正在初始化中，检测到循环动态请求`);
    }
    if (record.state === "failed") {
      throw record.failure instanceof Error ? record.failure : new Error(String(record.failure));
    }
    if (record.state === "unloaded") throw new Error(`插件 ${name} 已被卸载，不能再次初始化`);

    for (const dep of Object.keys(record.module.manifest.requires ?? {})) {
      const depRecord = this.records.get(dep);
      if (!depRecord || depRecord.state === "failed" || depRecord.state === "unloaded") {
        const error = new Error(`无法初始化 ${name}：前置插件 ${dep} 不可用`);
        record.state = "failed";
        record.failure = error;
        throw error;
      }
      if (depRecord.state !== "active") {
        try {
          this.initialize(dep);
        } catch {
          const error = new Error(`无法初始化 ${name}：前置插件 ${dep} 初始化失败`);
          record.state = "failed";
          record.failure = error;
          throw error;
        }
      }
    }

    record.state = "initializing";
    this.initCounts.set(name, (this.initCounts.get(name) ?? 0) + 1);
    try {
      record.instance = record.module.init(this.makeContext(name));
    } catch (error) {
      record.state = "failed";
      record.failure = error;
      this.unloadDependents(name);
      throw error;
    }
    record.state = "active";
    return record.instance;
  }

  unload(name: string): void {
    const record = this.records.get(name);
    if (!record) return;
    this.unloadDependents(name);
    if (record.state === "active") this.disposeRecord(name, record);
  }

  private unloadDependents(name: string): void {
    for (const dependent of this.activeHardDependentsOf(name)) {
      const record = this.records.get(dependent);
      if (record && record.state === "active") this.disposeRecord(dependent, record);
    }
  }

  private activeHardDependentsOf(name: string): string[] {
    const affected = new Set<string>();
    const stack = [name];
    while (stack.length > 0) {
      const current = stack.pop()!;
      for (const [candidate, module] of this.plan.selected) {
        if (affected.has(candidate)) continue;
        const requires = module.manifest.requires ?? {};
        if (Object.prototype.hasOwnProperty.call(requires, current) && this.plan.selected.has(current)) {
          affected.add(candidate);
          stack.push(candidate);
        }
      }
    }
    return [...this.plan.order].reverse().filter((candidate) => affected.has(candidate));
  }

  private disposeRecord(name: string, record: PluginRecord): void {
    try {
      record.module.dispose?.();
    } finally {
      this.disposeCounts.set(name, (this.disposeCounts.get(name) ?? 0) + 1);
      record.state = "unloaded";
      record.instance = undefined;
    }
  }

  private makeContext(name: string): PluginContext {
    return {
      pluginName: name,
      get: (dep: string) => this.getInstance(dep),
      require: (dep: string) => this.dynamicRequire(name, dep),
    };
  }

  private dynamicRequire(requester: string, dep: string): unknown {
    if (!this.records.has(dep) && this.registry) {
      const planner = new Planner(this.registry);
      const before = new Set(this.plan.selected.keys());
      if (planner.extend(this.plan, { name: dep })) {
        for (const [name, module] of this.plan.selected) {
          if (!before.has(name)) this.records.set(name, { module, state: "planned" });
        }
      }
    }
    const record = this.records.get(dep);
    if (!record) {
      throw new Error(`插件 ${requester} 动态请求的 ${dep} 不在计划中，也无法从注册表解析`);
    }
    if (record.state === "active") return record.instance;
    return this.initialize(dep);
  }
}
