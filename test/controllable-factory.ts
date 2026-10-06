import type { PluginDefinition, PluginFactory } from '../src/plugin-host';

export class Deferred<T> {
  readonly promise: Promise<T>;
  private resolveFn!: (value: T | PromiseLike<T>) => void;
  private rejectFn!: (reason?: unknown) => void;

  constructor() {
    this.promise = new Promise<T>((resolve, reject) => {
      this.resolveFn = resolve;
      this.rejectFn = reject;
    });
  }

  resolve(value: T): void {
    this.resolveFn(value);
  }

  reject(reason: unknown): void {
    this.rejectFn(reason);
  }
}

export class ControllableFactory {
  readonly events: string[] = [];
  readonly depsSeen = new Map<string, Readonly<Record<string, unknown>>>();
  private readonly initCounts = new Map<string, number>();
  private readonly gates = new Map<string, Deferred<unknown>>();
  private readonly initFailures = new Map<string, unknown>();
  private readonly disposeFailures = new Map<string, unknown>();

  initCount(name: string): number {
    return this.initCounts.get(name) ?? 0;
  }

  gate(name: string): void {
    this.gates.set(name, new Deferred<unknown>());
  }

  release(name: string, value?: unknown): void {
    const gate = this.gates.get(name);
    if (gate !== undefined) {
      this.gates.delete(name);
      gate.resolve(value);
    }
  }

  rejectGate(name: string, reason: unknown): void {
    const gate = this.gates.get(name);
    if (gate !== undefined) {
      this.gates.delete(name);
      gate.reject(reason);
    }
  }

  failInit(name: string, error: unknown): void {
    this.initFailures.set(name, error);
  }

  failDispose(name: string, error: unknown): void {
    this.disposeFailures.set(name, error);
  }

  define(name: string, dependencies: string[] = []): PluginDefinition {
    return {
      name,
      dependencies,
      factory: this.createFactory(name),
      dispose: () => this.runDispose(name),
    };
  }

  disposeEvents(): string[] {
    return this.events.filter((event) => event.startsWith('dispose:'));
  }

  private createFactory(name: string): PluginFactory {
    return async (context) => {
      this.events.push(`init:${name}`);
      this.initCounts.set(name, this.initCount(name) + 1);
      this.depsSeen.set(name, context.deps);
      if (this.initFailures.has(name)) {
        this.events.push(`fail:${name}`);
        throw this.initFailures.get(name);
      }
      const gate = this.gates.get(name);
      const value = gate !== undefined ? await gate.promise : undefined;
      this.events.push(`ready:${name}`);
      return value !== undefined ? value : { name };
    };
  }

  private async runDispose(name: string): Promise<void> {
    this.events.push(`dispose:${name}`);
    if (this.disposeFailures.has(name)) {
      throw this.disposeFailures.get(name);
    }
  }
}
