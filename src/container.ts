import { AsyncLocalStorage } from 'node:async_hooks';

export class CycleError extends Error {
  readonly chain: readonly string[];

  constructor(chain: readonly string[]) {
    super(`circular dependency detected: ${chain.join(' -> ')}`);
    this.name = 'CycleError';
    this.chain = chain;
  }
}

export class UnknownServiceError extends Error {
  constructor(name: string) {
    super(`unknown service: ${name}`);
    this.name = 'UnknownServiceError';
  }
}

export interface Resolver {
  get<T>(name: string): Promise<T>;
}

export type Factory<T> = (resolver: Resolver) => T | Promise<T>;
export type Disposer<T> = (instance: T) => void | Promise<void>;

export interface ServiceDefinition<T = unknown> {
  factory: Factory<T>;
  dispose?: Disposer<T>;
}

interface ResolutionContext {
  chain: string[];
}

interface ServiceRecord {
  state: 'idle' | 'constructing' | 'ready';
  generation: number;
  promise: Promise<unknown> | null;
  instance: unknown;
  waitingOn: string | null;
  completedAt: number;
}

export class Container {
  private readonly definitions = new Map<string, ServiceDefinition>();
  private readonly records = new Map<string, ServiceRecord>();
  private readonly storage = new AsyncLocalStorage<ResolutionContext>();
  private completionClock = 0;

  register<T>(name: string, definition: ServiceDefinition<T>): this {
    if (this.records.get(name)?.state !== undefined && this.records.get(name)?.state !== 'idle') {
      throw new Error(`cannot re-register "${name}" while it is constructing or ready`);
    }
    this.definitions.set(name, definition as ServiceDefinition);
    return this;
  }

  has(name: string): boolean {
    return this.definitions.has(name);
  }

  isReady(name: string): boolean {
    return this.records.get(name)?.state === 'ready';
  }

  async get<T>(name: string): Promise<T> {
    const context = this.storage.getStore();
    return this.resolve<T>(name, context?.chain ?? []);
  }

  async dispose(): Promise<void> {
    const inflight: Promise<unknown>[] = [];
    for (const record of this.records.values()) {
      if (record.state === 'constructing' && record.promise) {
        inflight.push(record.promise);
      }
    }
    await Promise.allSettled(inflight);

    const ready = [...this.records.entries()]
      .filter((entry): entry is [string, ServiceRecord] => entry[1].state === 'ready')
      .sort((a, b) => b[1].completedAt - a[1].completedAt);

    const errors: unknown[] = [];
    for (const [name, record] of ready) {
      const instance = record.instance;
      record.state = 'idle';
      record.instance = undefined;
      record.promise = null;
      const dispose = this.definitions.get(name)?.dispose;
      if (dispose) {
        try {
          await dispose(instance);
        } catch (error) {
          errors.push(error);
        }
      }
    }
    if (errors.length > 0) {
      throw new AggregateError(errors, `${errors.length} disposer(s) failed`);
    }
  }

  private async resolve<T>(name: string, chain: string[]): Promise<T> {
    const definition = this.definitions.get(name);
    if (!definition) {
      throw new UnknownServiceError(name);
    }

    const cycleStart = chain.indexOf(name);
    if (cycleStart !== -1) {
      throw new CycleError([...chain.slice(cycleStart), name]);
    }

    const record = this.recordFor(name);

    if (record.state === 'ready') {
      return record.instance as T;
    }

    if (record.state === 'constructing') {
      const top = chain[chain.length - 1];
      if (top === undefined) {
        return record.promise as Promise<T>;
      }
      const topRecord = this.recordFor(top);
      const topGeneration = topRecord.generation;
      topRecord.waitingOn = name;
      try {
        const loop = this.findLoop(name, top);
        if (loop) {
          throw new CycleError(loop);
        }
        return (await record.promise) as T;
      } finally {
        if (topRecord.generation === topGeneration) {
          topRecord.waitingOn = null;
        }
      }
    }

    return this.construct<T>(name, definition as ServiceDefinition<T>, record, chain);
  }

  private construct<T>(
    name: string,
    definition: ServiceDefinition<T>,
    record: ServiceRecord,
    chain: string[],
  ): Promise<T> {
    const generation = record.generation + 1;
    record.generation = generation;
    record.state = 'constructing';
    record.waitingOn = null;

    const childChain = [...chain, name];
    const resolver: Resolver = {
      get: <U>(dependency: string) => this.resolve<U>(dependency, childChain),
    };

    const promise = this.storage.run({ chain: childChain }, async () => {
      try {
        const instance = await definition.factory(resolver);
        record.instance = instance;
        record.state = 'ready';
        record.completedAt = ++this.completionClock;
        return instance;
      } catch (error) {
        if (record.generation === generation) {
          record.state = 'idle';
          record.instance = undefined;
          record.promise = null;
        }
        throw error;
      }
    });
    record.promise = promise;
    return promise as Promise<T>;
  }

  private findLoop(from: string, target: string): string[] | null {
    const path = [target, from];
    const visited = new Set(path);
    let current = from;
    for (;;) {
      const next = this.records.get(current)?.waitingOn;
      if (next === undefined || next === null) {
        return null;
      }
      if (next === target) {
        return [...path, target];
      }
      if (visited.has(next)) {
        return null;
      }
      visited.add(next);
      path.push(next);
      current = next;
    }
  }

  private recordFor(name: string): ServiceRecord {
    let record = this.records.get(name);
    if (!record) {
      record = {
        state: 'idle',
        generation: 0,
        promise: null,
        instance: undefined,
        waitingOn: null,
        completedAt: 0,
      };
      this.records.set(name, record);
    }
    return record;
  }
}
