import { AsyncLocalStorage } from "node:async_hooks";

export type Token<T = unknown> = (string | symbol) & { __t?: T };

export interface Resolver {
  get<T>(token: Token<T>): Promise<T>;
}

export type Factory<T> = (resolver: Resolver) => T | Promise<T>;
export type Disposer<T> = (value: T) => void | Promise<void>;

const tokenName = (token: Token): string =>
  typeof token === "symbol" ? token.toString() : token;

const formatChain = (chain: readonly Token[]): string =>
  chain.map(tokenName).join(" -> ");

export class DependencyCycleError extends Error {
  readonly chain: readonly Token[];

  constructor(chain: readonly Token[]) {
    super(`Dependency cycle detected: ${formatChain(chain)}`);
    this.name = "DependencyCycleError";
    this.chain = chain;
  }
}

type RecordState<T> =
  | { kind: "idle" }
  | { kind: "building"; promise: Promise<T> }
  | { kind: "ready"; value: T };

interface RecordEntry<T> {
  factory: Factory<T>;
  dispose?: Disposer<T>;
  generation: number;
  state: RecordState<T>;
}

/**
 * Walks the wait-for graph starting at `from`, looking for a path back to
 * `target`. Returns the closed loop (target ... target) when found.
 */
function findWaitLoop(
  edges: ReadonlyMap<Token, Token>,
  from: Token,
  target: Token,
): Token[] | null {
  const path: Token[] = [target, from];
  const seen = new Set<Token>(path);
  let current = from;
  for (;;) {
    const next = edges.get(current);
    if (next === undefined) return null;
    if (next === target) return [...path, target];
    if (seen.has(next)) return null;
    seen.add(next);
    path.push(next);
    current = next;
  }
}

export class Container {
  readonly #records = new Map<Token, RecordEntry<unknown>>();
  readonly #chain = new AsyncLocalStorage<Token[]>();
  /** Wait-for edges between in-flight builds: token -> token it awaits. */
  readonly #waitingOn = new Map<Token, Token>();
  /** Constructed resources in completion order, disposed in reverse. */
  readonly #constructed: Array<{ value: unknown; dispose: Disposer<unknown> }> = [];
  #disposed = false;

  register<T>(token: Token<T>, factory: Factory<T>, dispose?: Disposer<T>): this {
    if (this.#disposed) throw new Error("Container is disposed");
    if (this.#records.has(token)) {
      throw new Error(`Duplicate registration for ${tokenName(token)}`);
    }
    this.#records.set(token, {
      factory: factory as Factory<unknown>,
      dispose: dispose as Disposer<unknown> | undefined,
      generation: 0,
      state: { kind: "idle" },
    });
    return this;
  }

  get<T>(token: Token<T>): Promise<T> {
    if (this.#disposed) {
      return Promise.reject(
        new Error(`Container is disposed; cannot resolve ${tokenName(token)}`),
      );
    }
    const record = this.#records.get(token) as RecordEntry<T> | undefined;
    if (!record) {
      return Promise.reject(
        new Error(`No provider registered for ${tokenName(token)}`),
      );
    }

    const chain = this.#chain.getStore() ?? [];

    // Cycle within this request's own resolution chain.
    const ownIndex = chain.indexOf(token);
    if (ownIndex !== -1) {
      return Promise.reject(
        new DependencyCycleError([...chain.slice(ownIndex), token]),
      );
    }

    const state = record.state;
    if (state.kind === "ready") return Promise.resolve(state.value);

    if (state.kind === "building") {
      // Cross-request cycle: another chain is already building this token.
      // Add a wait-for edge and reject if it closes a loop.
      const requester = chain[chain.length - 1];
      if (requester !== undefined) {
        this.#waitingOn.set(requester, token);
        const loop = findWaitLoop(this.#waitingOn, token, requester);
        if (loop) {
          this.#waitingOn.delete(requester);
          return Promise.reject(new DependencyCycleError(loop));
        }
      }
      return state.promise;
    }

    // Idle: start a new construction generation.
    record.generation += 1;
    const nextChain = [...chain, token];
    const promise = (async (): Promise<T> => {
      try {
        const value = await this.#chain.run(nextChain, () =>
          record.factory(this.#resolver),
        );
        record.state = { kind: "ready", value };
        if (record.dispose) {
          this.#constructed.push({ value, dispose: record.dispose });
        }
        return value;
      } catch (error) {
        // Failed generation: reset to idle so a later request retries.
        record.state = { kind: "idle" };
        throw error;
      } finally {
        this.#waitingOn.delete(token);
      }
    })();
    record.state = { kind: "building", promise };
    return promise;
  }

  readonly #resolver: Resolver = {
    get: <T>(token: Token<T>): Promise<T> => this.get(token),
  };

  /** Disposes constructed resources in reverse dependency order. Idempotent. */
  async dispose(): Promise<void> {
    if (this.#disposed) return;
    this.#disposed = true;
    const constructed = this.#constructed.splice(0);
    for (let index = constructed.length - 1; index >= 0; index -= 1) {
      const { value, dispose } = constructed[index];
      await dispose(value);
    }
  }
}
