export interface PluginContext {
  readonly host: PluginHost;
  readonly deps: Readonly<Record<string, unknown>>;
}

export type PluginFactory = (context: PluginContext) => unknown | Promise<unknown>;

export type DisposeHook = (instance: unknown) => void | Promise<void>;

export interface PluginDefinition {
  readonly name: string;
  readonly dependencies?: readonly string[];
  readonly factory: PluginFactory;
  readonly dispose?: DisposeHook;
}

export interface CleanupFailure {
  readonly plugin: string;
  readonly error: unknown;
}

export class UnknownPluginError extends Error {
  readonly plugin: string;

  constructor(plugin: string) {
    super(`unknown plugin "${plugin}"`);
    this.name = 'UnknownPluginError';
    this.plugin = plugin;
  }
}

export class PluginCycleError extends Error {
  readonly chain: readonly string[];

  constructor(chain: readonly string[]) {
    super(`circular plugin dependency: ${chain.join(' -> ')}`);
    this.name = 'PluginCycleError';
    this.chain = chain;
  }
}

export class PluginInstallError extends Error {
  readonly plugin: string;
  readonly cause: unknown;
  readonly rollbackFailures: readonly CleanupFailure[];

  constructor(plugin: string, cause: unknown, rollbackFailures: readonly CleanupFailure[]) {
    super(`failed to install plugin "${plugin}": ${describeError(cause)}`);
    this.name = 'PluginInstallError';
    this.plugin = plugin;
    this.cause = cause;
    this.rollbackFailures = rollbackFailures;
  }
}

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

type RecordState = 'initializing' | 'ready' | 'failed' | 'disposed';

interface PluginRecord {
  readonly name: string;
  readonly definition: PluginDefinition;
  state: RecordState;
  instance: unknown;
  promise: Promise<unknown>;
  readonly dependents: Set<string>;
  rootCount: number;
  readonly depEdges: PluginRecord[];
}

interface InstallOperation {
  readonly created: PluginRecord[];
}

export class PluginHost {
  private readonly definitions = new Map<string, PluginDefinition>();
  private readonly records = new Map<string, PluginRecord>();
  private readonly waitsOn = new Map<PluginRecord, Set<PluginRecord>>();

  register(definition: PluginDefinition): this {
    if (this.definitions.has(definition.name)) {
      throw new Error(`plugin "${definition.name}" is already registered`);
    }
    this.definitions.set(definition.name, definition);
    return this;
  }

  registerAll(definitions: Iterable<PluginDefinition>): this {
    for (const definition of definitions) {
      this.register(definition);
    }
    return this;
  }

  isReady(name: string): boolean {
    const record = this.records.get(name);
    return record !== undefined && record.state === 'ready';
  }

  getInstance(name: string): unknown {
    const record = this.records.get(name);
    return record !== undefined && record.state === 'ready' ? record.instance : undefined;
  }

  refCount(name: string): number {
    const record = this.records.get(name);
    if (record === undefined || record.state !== 'ready') {
      return 0;
    }
    return record.rootCount + record.dependents.size;
  }

  async install(name: string): Promise<unknown> {
    const operation: InstallOperation = { created: [] };
    let record: PluginRecord;
    try {
      record = await this.ensureReady(name, operation, [name]);
    } catch (error) {
      const rollbackFailures = await this.rollbackCreated(operation);
      throw new PluginInstallError(name, error, rollbackFailures);
    }
    record.rootCount += 1;
    return record.instance;
  }

  installAll(names: Iterable<string>): Promise<unknown[]> {
    return Promise.all(Array.from(names, (name) => this.install(name)));
  }

  async uninstall(name: string): Promise<CleanupFailure[]> {
    const record = this.records.get(name);
    if (record === undefined || record.rootCount === 0) {
      throw new Error(`plugin "${name}" is not installed`);
    }
    record.rootCount -= 1;
    const failures: CleanupFailure[] = [];
    await this.releaseIfUnused(record, failures);
    return failures;
  }

  async shutdown(): Promise<CleanupFailure[]> {
    const failures: CleanupFailure[] = [];
    for (;;) {
      const next = this.findDisposableSink();
      if (next === undefined) {
        break;
      }
      next.rootCount = 0;
      await this.evict(next, failures);
    }
    return failures;
  }

  private findDisposableSink(): PluginRecord | undefined {
    for (const record of this.records.values()) {
      if (record.state !== 'ready') {
        continue;
      }
      let hasReadyDependent = false;
      for (const dependentName of record.dependents) {
        const dependent = this.records.get(dependentName);
        if (dependent !== undefined && dependent.state === 'ready') {
          hasReadyDependent = true;
          break;
        }
      }
      if (!hasReadyDependent) {
        return record;
      }
    }
    return undefined;
  }

  private async ensureReady(
    name: string,
    operation: InstallOperation,
    chain: readonly string[],
  ): Promise<PluginRecord> {
    const record = this.ensureRecord(name, operation, chain);
    await record.promise;
    return record;
  }

  private ensureRecord(
    name: string,
    operation: InstallOperation,
    chain: readonly string[],
  ): PluginRecord {
    const existing = this.records.get(name);
    if (existing !== undefined) {
      return existing;
    }
    const definition = this.definitions.get(name);
    if (definition === undefined) {
      throw new UnknownPluginError(name);
    }
    const record: PluginRecord = {
      name,
      definition,
      state: 'initializing',
      instance: undefined,
      promise: Promise.resolve(),
      dependents: new Set<string>(),
      rootCount: 0,
      depEdges: [],
    };
    this.records.set(name, record);
    operation.created.push(record);
    record.promise = this.initRecord(record, operation, chain);
    return record;
  }

  private async initRecord(
    record: PluginRecord,
    operation: InstallOperation,
    chain: readonly string[],
  ): Promise<unknown> {
    const deps: Record<string, unknown> = {};
    try {
      for (const depName of (record.definition.dependencies ?? [])) {
        if (chain.includes(depName)) {
          throw new PluginCycleError([...chain, depName]);
        }
        const depRecord = this.ensureRecord(depName, operation, [...chain, depName]);
        record.depEdges.push(depRecord);
        depRecord.dependents.add(record.name);
        deps[depName] = await this.awaitDependency(record, depRecord);
      }
      const instance = await record.definition.factory({ host: this, deps });
      record.instance = instance;
      record.state = 'ready';
      return instance;
    } catch (error) {
      record.state = 'failed';
      if (this.records.get(record.name) === record) {
        this.records.delete(record.name);
      }
      for (const dep of record.depEdges) {
        dep.dependents.delete(record.name);
      }
      throw error;
    }
  }

  private async awaitDependency(record: PluginRecord, depRecord: PluginRecord): Promise<unknown> {
    if (this.wouldDeadlock(record, depRecord)) {
      throw new PluginCycleError([record.name, depRecord.name, record.name]);
    }
    let edges = this.waitsOn.get(record);
    if (edges === undefined) {
      edges = new Set<PluginRecord>();
      this.waitsOn.set(record, edges);
    }
    edges.add(depRecord);
    try {
      return await depRecord.promise;
    } finally {
      edges.delete(depRecord);
    }
  }

  private wouldDeadlock(from: PluginRecord, to: PluginRecord): boolean {
    const stack: PluginRecord[] = [to];
    const seen = new Set<PluginRecord>();
    while (stack.length > 0) {
      const current = stack.pop() as PluginRecord;
      if (current === from) {
        return true;
      }
      if (seen.has(current)) {
        continue;
      }
      seen.add(current);
      const edges = this.waitsOn.get(current);
      if (edges !== undefined) {
        for (const target of edges) {
          stack.push(target);
        }
      }
    }
    return false;
  }

  private async rollbackCreated(operation: InstallOperation): Promise<CleanupFailure[]> {
    const failures: CleanupFailure[] = [];
    for (let index = operation.created.length - 1; index >= 0; index -= 1) {
      const record = operation.created[index];
      if (record.state !== 'ready') {
        continue;
      }
      if (record.rootCount > 0 || record.dependents.size > 0) {
        continue;
      }
      await this.evict(record, failures);
    }
    return failures;
  }

  private async releaseIfUnused(record: PluginRecord, failures: CleanupFailure[]): Promise<void> {
    if (record.state !== 'ready') {
      return;
    }
    if (record.rootCount > 0 || record.dependents.size > 0) {
      return;
    }
    await this.evict(record, failures);
    for (const dep of record.depEdges) {
      await this.releaseIfUnused(dep, failures);
    }
  }

  private async evict(record: PluginRecord, failures: CleanupFailure[]): Promise<void> {
    if (this.records.get(record.name) === record) {
      this.records.delete(record.name);
    }
    record.state = 'disposed';
    for (const dep of record.depEdges) {
      dep.dependents.delete(record.name);
    }
    try {
      await this.disposeInstance(record);
    } catch (error) {
      failures.push({ plugin: record.name, error });
    }
  }

  private async disposeInstance(record: PluginRecord): Promise<void> {
    const hook = record.definition.dispose;
    if (hook !== undefined) {
      await hook(record.instance);
      return;
    }
    const instance = record.instance as { dispose?: () => void | Promise<void> } | null | undefined;
    if (instance !== null && instance !== undefined && typeof instance.dispose === 'function') {
      await instance.dispose();
    }
  }
}
