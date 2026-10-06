/**
 * A plugin host that initializes plugins in dependency order with
 * reference-counted shared dependencies.
 *
 * Semantics:
 * - install(): resolves the dependency closure, initializes plugins
 *   dependencies-first. Concurrent installs share in-flight
 *   initializations of the same plugin.
 * - On failure only references acquired by the failing install are
 *   released; instances still referenced by other installs (including
 *   pre-existing shared dependencies) are kept.
 * - uninstall()/dispose(): cleanup runs in reverse dependency order.
 *   Cleanup failures are collected and reported together after all
 *   remaining cleanups have been attempted.
 */

export interface PluginContext {
  /** Returns the initialized instance of a declared dependency. */
  get<T = unknown>(name: string): T;
}

export type PluginFactory<T> = (context: PluginContext) => T | Promise<T>;
export type PluginDispose<T> = (instance: T) => void | Promise<void>;

export interface PluginDefinition<T = unknown> {
  readonly name: string;
  readonly dependencies?: readonly string[];
  readonly factory: PluginFactory<T>;
  readonly dispose?: PluginDispose<T>;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyPluginDefinition = PluginDefinition<any>;

export interface InstallHandle {
  /** Names of every plugin this install references, dependencies first. */
  readonly plugins: readonly string[];
}

export interface CleanupFailure {
  readonly plugin: string;
  readonly error: unknown;
}

/** Thrown by uninstall()/dispose() after all cleanups were attempted. */
export class CleanupError extends Error {
  readonly failures: readonly CleanupFailure[];

  constructor(failures: readonly CleanupFailure[]) {
    super(
      `cleanup failed for ${failures.length} plugin(s): ` +
        failures.map((f) => f.plugin).join(', '),
    );
    this.name = 'CleanupError';
    this.failures = failures;
  }
}

/** Thrown by install() when a plugin factory fails, after rollback. */
export class PluginInstallError extends Error {
  readonly plugin: string;
  readonly cause: unknown;
  readonly rollbackFailures: readonly CleanupFailure[];

  constructor(
    plugin: string,
    cause: unknown,
    rollbackFailures: readonly CleanupFailure[],
  ) {
    super(
      `failed to initialize plugin "${plugin}": ${
        cause instanceof Error ? cause.message : String(cause)
      }`,
    );
    this.name = 'PluginInstallError';
    this.plugin = plugin;
    this.cause = cause;
    this.rollbackFailures = rollbackFailures;
  }
}

export class PluginCycleError extends Error {
  constructor(readonly cycle: readonly string[]) {
    super(`dependency cycle detected: ${cycle.join(' -> ')}`);
    this.name = 'PluginCycleError';
  }
}

export class UnknownPluginError extends Error {
  constructor(name: string, requiredBy?: string) {
    super(
      requiredBy === undefined
        ? `unknown plugin "${name}"`
        : `plugin "${requiredBy}" depends on unknown plugin "${name}"`,
    );
    this.name = 'UnknownPluginError';
  }
}

type PluginStatus = 'registered' | 'initializing' | 'ready' | 'failed';

interface PluginRecord {
  definition: AnyPluginDefinition;
  status: PluginStatus;
  instance: unknown;
  /** Shared by concurrent installs while status === 'initializing'. */
  initPromise: Promise<unknown> | null;
  /** Number of installs currently holding a reference. */
  refCount: number;
}

export class PluginHost {
  private readonly records = new Map<string, PluginRecord>();

  /**
   * Registers definitions (re-registering an already known name is a
   * no-op) and initializes the requested plugins plus their transitive
   * dependencies in dependency order.
   *
   * References are acquired eagerly, so a failed install releases
   * exactly what it acquired: instances it created are disposed
   * (reverse order) unless another install still references them.
   */
  async install(
    definitions: AnyPluginDefinition | readonly AnyPluginDefinition[],
  ): Promise<InstallHandle> {
    const defs = Array.isArray(definitions)
      ? (definitions as readonly AnyPluginDefinition[])
      : [definitions as AnyPluginDefinition];
    for (const def of defs) {
      if (!this.records.has(def.name)) {
        this.records.set(def.name, {
          definition: def,
          status: 'registered',
          instance: undefined,
          initPromise: null,
          refCount: 0,
        });
      }
    }

    const order = this.resolveOrder(defs.map((d) => d.name));
    const acquired: string[] = [];

    try {
      for (const name of order) {
        const record = this.requireRecord(name);
        record.refCount += 1;
        acquired.push(name);
        await this.ensureInitialized(record);
      }
    } catch (error) {
      const rollbackFailures = await this.releaseAll(acquired);
      if (error instanceof PluginInstallError) {
        throw new PluginInstallError(
          error.plugin,
          error.cause,
          [...error.rollbackFailures, ...rollbackFailures],
        );
      }
      throw error;
    }

    return { plugins: order };
  }

  /**
   * Releases the references held by a handle. Plugins whose reference
   * count drops to zero are disposed in reverse dependency order.
   * Disposal failures are collected and reported as a CleanupError
   * after every remaining disposal has been attempted.
   */
  async uninstall(handle: InstallHandle): Promise<void> {
    const failures = await this.releaseAll([...handle.plugins]);
    if (failures.length > 0) {
      throw new CleanupError(failures);
    }
  }

  /** Disposes every initialized plugin in reverse dependency order. */
  async dispose(): Promise<void> {
    const ready = [...this.records.values()]
      .filter((r) => r.status === 'ready')
      .map((r) => r.definition.name);
    const order = this.resolveOrder(ready);
    const failures: CleanupFailure[] = [];
    for (const name of order.reverse()) {
      const record = this.requireRecord(name);
      if (record.status !== 'ready') {
        continue;
      }
      record.refCount = 0;
      const failure = await this.disposeRecord(record);
      if (failure !== null) {
        failures.push(failure);
      }
    }
    if (failures.length > 0) {
      throw new CleanupError(failures);
    }
  }

  /** Current reference count of a plugin (0 if unknown). */
  refCount(name: string): number {
    return this.records.get(name)?.refCount ?? 0;
  }

  /** Whether a plugin is initialized and available. */
  isReady(name: string): boolean {
    return this.records.get(name)?.status === 'ready';
  }

  /** Returns the instance of a ready plugin. */
  get<T = unknown>(name: string): T {
    const record = this.records.get(name);
    if (record === undefined) {
      throw new UnknownPluginError(name);
    }
    if (record.status !== 'ready') {
      throw new Error(`plugin "${name}" is not initialized (status: ${record.status})`);
    }
    return record.instance as T;
  }

  private requireRecord(name: string): PluginRecord {
    const record = this.records.get(name);
    if (record === undefined) {
      throw new UnknownPluginError(name);
    }
    return record;
  }

  /** Topological order (dependencies first) of the closure of `names`. */
  private resolveOrder(names: readonly string[]): string[] {
    const order: string[] = [];
    const state = new Map<string, 'visiting' | 'done'>();
    const stack: string[] = [];

    const visit = (name: string, requiredBy?: string): void => {
      const mark = state.get(name);
      if (mark === 'done') {
        return;
      }
      if (mark === 'visiting') {
        const cycleStart = stack.indexOf(name);
        throw new PluginCycleError([...stack.slice(cycleStart), name]);
      }
      const record = this.records.get(name);
      if (record === undefined) {
        throw new UnknownPluginError(name, requiredBy);
      }
      state.set(name, 'visiting');
      stack.push(name);
      for (const dep of record.definition.dependencies ?? []) {
        visit(dep, name);
      }
      stack.pop();
      state.set(name, 'done');
      order.push(name);
    };

    for (const name of names) {
      visit(name);
    }
    return order;
  }

  private async ensureInitialized(record: PluginRecord): Promise<void> {
    if (record.status === 'ready') {
      return;
    }
    if (record.status === 'initializing' && record.initPromise !== null) {
      // Concurrent install: share the in-flight initialization.
      // A rejection propagates to every waiter as a PluginInstallError
      // for the same plugin, mirroring what the creator observes.
      try {
        await record.initPromise;
      } catch (cause) {
        throw new PluginInstallError(record.definition.name, cause, []);
      }
      return;
    }
    // 'registered' or 'failed': (re)start initialization.
    record.status = 'initializing';
    record.initPromise = this.runFactory(record);
    try {
      record.instance = await record.initPromise;
      record.status = 'ready';
    } catch (cause) {
      record.status = 'failed';
      record.instance = undefined;
      throw new PluginInstallError(record.definition.name, cause, []);
    } finally {
      record.initPromise = null;
    }
  }

  private async runFactory(record: PluginRecord): Promise<unknown> {
    const context: PluginContext = {
      get: <T>(name: string): T => this.get<T>(name),
    };
    return record.definition.factory(context);
  }

  /**
   * Releases references in reverse order, disposing instances whose
   * count reaches zero. Failures are collected, never thrown, so every
   * remaining release still runs.
   */
  private async releaseAll(names: readonly string[]): Promise<CleanupFailure[]> {
    const failures: CleanupFailure[] = [];
    for (const name of [...names].reverse()) {
      const record = this.records.get(name);
      if (record === undefined || record.refCount <= 0) {
        continue;
      }
      record.refCount -= 1;
      if (record.refCount === 0 && record.status === 'ready') {
        const failure = await this.disposeRecord(record);
        if (failure !== null) {
          failures.push(failure);
        }
      }
    }
    return failures;
  }

  private async disposeRecord(record: PluginRecord): Promise<CleanupFailure | null> {
    const name = record.definition.name;
    const instance = record.instance;
    record.status = 'registered';
    record.instance = undefined;
    record.initPromise = null;
    try {
      const dispose = record.definition.dispose;
      if (dispose !== undefined) {
        await dispose(instance);
      } else if (
        instance !== null &&
        typeof instance === 'object' &&
        typeof (instance as { dispose?: unknown }).dispose === 'function'
      ) {
        await (instance as { dispose: () => void | Promise<void> }).dispose();
      }
      return null;
    } catch (error) {
      return { plugin: name, error };
    }
  }
}
