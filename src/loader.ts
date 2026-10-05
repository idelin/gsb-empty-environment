import type { PluginManifest } from './manifest.ts';
import type { LoadPlan } from './planner.ts';

export type PluginState =
  | 'loaded'
  | 'initializing'
  | 'initialized'
  | 'started'
  | 'stopped'
  | 'unloaded'
  | 'failed';

export interface PluginContext {
  readonly pluginName: string;
  /** 动态请求插件：已初始化则直接返回实例；正在初始化则复用同一 Promise，绝不重复初始化 */
  require<T = unknown>(name: string): Promise<T>;
  /** 仅当目标已初始化时返回实例，否则 undefined */
  get<T = unknown>(name: string): T | undefined;
}

export interface PluginModule {
  init?(context: PluginContext): unknown | Promise<unknown>;
  start?(): unknown | Promise<unknown>;
  stop?(): unknown | Promise<unknown>;
  dispose?(): unknown | Promise<unknown>;
}

export type PluginFactory = (manifest: PluginManifest) => PluginModule;

interface PluginRecord {
  manifest: PluginManifest;
  module: PluginModule;
  state: PluginState;
  instance?: unknown;
  error?: unknown;
  cause?: string;
  initPromise?: Promise<unknown>;
}

export interface InitReport {
  initialized: string[];
  failed: { name: string; error: unknown }[];
  unloaded: { name: string; cause: string }[];
}

export class PluginLoader {
  readonly plan: LoadPlan;
  private readonly records = new Map<string, PluginRecord>();
  /** dep -> 依赖它的插件（静态 requires + 运行时动态 require 边） */
  private readonly dependents = new Map<string, Set<string>>();
  private readonly initChain: string[] = [];

  constructor(plan: LoadPlan, modules: Record<string, PluginModule | PluginFactory>) {
    this.plan = plan;
    for (const manifest of plan.order) {
      const source = modules[manifest.name];
      if (!source) throw new Error(`缺少插件 ${manifest.name} 的模块实现`);
      const module = typeof source === 'function' ? source(manifest) : source;
      this.records.set(manifest.name, { manifest, module, state: 'loaded' });
    }
    for (const manifest of plan.order) {
      for (const dep of Object.keys(manifest.requires ?? {})) {
        if (this.records.has(dep)) this.dependentsOf(dep).add(manifest.name);
      }
    }
  }

  private dependentsOf(name: string): Set<string> {
    let set = this.dependents.get(name);
    if (!set) {
      set = new Set();
      this.dependents.set(name, set);
    }
    return set;
  }

  getState(name: string): PluginState | undefined {
    return this.records.get(name)?.state;
  }

  getPlugin<T = unknown>(name: string): T | undefined {
    const record = this.records.get(name);
    if (!record) return undefined;
    return record.state === 'initialized' || record.state === 'started' || record.state === 'stopped'
      ? (record.instance as T)
      : undefined;
  }

  private createContext(name: string): PluginContext {
    return {
      pluginName: name,
      require: <T,>(dep: string): Promise<T> => {
        if (!this.records.has(dep)) {
          return Promise.reject(new Error(`插件 ${name} 动态请求了未在加载计划中的插件 ${dep}`));
        }
        this.dependentsOf(dep).add(name);
        return this.ensureInitialized(dep) as Promise<T>;
      },
      get: <T,>(dep: string): T | undefined => this.getPlugin<T>(dep),
    };
  }

  private ensureInitialized(name: string): Promise<unknown> {
    const record = this.records.get(name);
    if (!record) return Promise.reject(new Error(`插件 ${name} 不在加载计划中`));
    if (record.state === 'initialized' || record.state === 'started' || record.state === 'stopped') {
      return Promise.resolve(record.instance);
    }
    if (record.state === 'initializing') {
      if (this.initChain.includes(name)) {
        return Promise.reject(new Error(`检测到循环动态请求: ${[...this.initChain, name].join(' -> ')}`));
      }
      return record.initPromise!;
    }
    if (record.state === 'failed' || record.state === 'unloaded') {
      return Promise.reject(new Error(`插件 ${name} 不可用（状态: ${record.state}）`));
    }
    record.state = 'initializing';
    const context = this.createContext(name);
    record.initPromise = (async () => {
      this.initChain.push(name);
      try {
        const result = await record.module.init?.(context);
        record.instance = result === undefined ? record.module : result;
        record.state = 'initialized';
        return record.instance;
      } catch (error) {
        record.state = 'loaded';
        record.initPromise = undefined;
        throw error;
      } finally {
        this.initChain.splice(this.initChain.indexOf(name), 1);
      }
    })();
    return record.initPromise;
  }

  /** 按计划顺序初始化；某个插件失败时只卸载依赖它的已加载部分，独立插件继续初始化 */
  async initAll(): Promise<InitReport> {
    const report: InitReport = { initialized: [], failed: [], unloaded: [] };
    for (const manifest of this.plan.order) {
      const record = this.records.get(manifest.name)!;
      if (record.state !== 'loaded') continue;
      try {
        await this.ensureInitialized(manifest.name);
        report.initialized.push(manifest.name);
      } catch (error) {
        await this.handleInitFailure(manifest.name, error, report);
      }
    }
    return report;
  }

  private async handleInitFailure(name: string, error: unknown, report: InitReport): Promise<void> {
    const record = this.records.get(name)!;
    record.state = 'failed';
    record.error = error;
    report.failed.push({ name, error });
    await this.safeCall(record.module, record.module.dispose);
    for (const victim of this.transitiveDependents(name)) {
      const vrec = this.records.get(victim)!;
      if (vrec.state === 'unloaded' || vrec.state === 'failed') continue;
      if (vrec.state === 'started') await this.safeCall(vrec.module, vrec.module.stop);
      if (vrec.state !== 'loaded') await this.safeCall(vrec.module, vrec.module.dispose);
      vrec.state = 'unloaded';
      vrec.cause = `依赖的插件 ${name} 初始化失败`;
      report.unloaded.push({ name: victim, cause: vrec.cause });
    }
  }

  /** 逆计划顺序返回所有（传递）依赖 name 的插件 */
  private transitiveDependents(name: string): string[] {
    const seen = new Set<string>();
    const stack = [name];
    while (stack.length > 0) {
      const current = stack.pop()!;
      for (const dependent of this.dependents.get(current) ?? []) {
        if (!seen.has(dependent)) {
          seen.add(dependent);
          stack.push(dependent);
        }
      }
    }
    const order = this.plan.order.map((m) => m.name);
    return [...seen].sort((a, b) => order.indexOf(b) - order.indexOf(a));
  }

  private async safeCall(module: PluginModule, fn?: () => unknown): Promise<void> {
    if (!fn) return;
    try {
      await fn.call(module);
    } catch {
      // 卸载 / 清理阶段的异常不再外抛，避免掩盖原始失败
    }
  }

  /** 按拓扑正序启动所有已初始化的插件 */
  async startAll(): Promise<void> {
    for (const manifest of this.plan.order) {
      const record = this.records.get(manifest.name)!;
      if (record.state !== 'initialized') continue;
      await record.module.start?.();
      record.state = 'started';
    }
  }

  /** 按拓扑逆序停止所有已启动的插件 */
  async stopAll(): Promise<void> {
    for (const manifest of [...this.plan.order].reverse()) {
      const record = this.records.get(manifest.name)!;
      if (record.state !== 'started') continue;
      await record.module.stop?.();
      record.state = 'stopped';
    }
  }
}
