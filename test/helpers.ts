import { PluginContext } from '../src/index';

export interface Deferred<T> {
  readonly promise: Promise<T>;
  resolve(value: T): void;
  reject(error: unknown): void;
}

export function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

export interface FactoryCall<T> {
  readonly context: PluginContext;
  readonly index: number;
  resolve(value: T): void;
  reject(error: unknown): void;
}

/**
 * A factory whose every invocation is parked until the test explicitly
 * resolves or rejects it. Used to observe concurrent installs while a
 * shared dependency is still initializing.
 */
export class ControllableFactory<T> {
  readonly calls: FactoryCall<T>[] = [];
  private readonly waiters: Array<(call: FactoryCall<T>) => void> = [];

  readonly factory = (context: PluginContext): Promise<T> => {
    const gate = deferred<T>();
    const call: FactoryCall<T> = {
      context,
      index: this.calls.length,
      resolve: gate.resolve,
      reject: gate.reject,
    };
    this.calls.push(call);
    const waiter = this.waiters.shift();
    if (waiter !== undefined) {
      waiter(call);
    }
    return gate.promise;
  };

  get callCount(): number {
    return this.calls.length;
  }

  /** Waits until the factory has been invoked at least `n` times. */
  async waitForCalls(n: number): Promise<void> {
    while (this.calls.length < n) {
      await new Promise<FactoryCall<T>>((resolve) => this.waiters.push(resolve));
    }
  }

  lastCall(): FactoryCall<T> {
    const call = this.calls[this.calls.length - 1];
    if (call === undefined) {
      throw new Error('factory was never called');
    }
    return call;
  }
}

/** Records lifecycle events so tests can assert ordering. */
export class EventLog {
  readonly events: string[] = [];

  record(event: string): void {
    this.events.push(event);
  }

  filtered(prefix: string): string[] {
    return this.events.filter((e) => e.startsWith(prefix));
  }
}

interface TestCase {
  name: string;
  fn: () => void | Promise<void>;
}

const tests: TestCase[] = [];

export function test(name: string, fn: () => void | Promise<void>): void {
  tests.push({ name, fn });
}

export async function runTests(): Promise<void> {
  let passed = 0;
  const failures: Array<{ name: string; error: unknown }> = [];
  for (const { name, fn } of tests) {
    try {
      await fn();
      passed += 1;
      console.log(`ok   ${name}`);
    } catch (error) {
      failures.push({ name, error });
      console.error(`FAIL ${name}`);
    }
  }
  console.log(`\n${passed}/${tests.length} passed`);
  for (const { name, error } of failures) {
    console.error(`\n--- ${name} ---`);
    console.error(error);
  }
  if (failures.length > 0) {
    process.exitCode = 1;
  }
}
