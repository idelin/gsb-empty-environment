import { AsyncLocalStorage } from "node:async_hooks";
import { CasConflict, type CasEntry, type CasStore } from "../store/cas.ts";

/** Identifies one logical agent (e.g. one browser window / proxy retry). */
export type AgentId = string;

const agentStorage = new AsyncLocalStorage<AgentId>();

/** Run an agent's work so every storage yield is attributed to it. */
export function runAsAgent<T>(agent: AgentId, task: () => Promise<T>): Promise<T> {
  return agentStorage.run(agent, task);
}

export function currentAgent(): AgentId {
  const agent = agentStorage.getStore();
  if (!agent) {
    throw new Error("storage operation outside of runAsAgent scope");
  }
  return agent;
}

interface PendingOp {
  readonly agent: AgentId;
  run: () => void;
}

/**
 * Deterministic interleaving harness.
 *
 * Each CAS read/write is split into "await a scheduler slot" followed by the
 * real atomic operation. `step()` resolves exactly one pending slot, so a
 * test can interleave two agents at individual write points with full
 * reproducibility instead of relying on OS thread timing.
 */
export class ScriptedCasStore implements CasStore {
  readonly #entries = new Map<string, { version: number; value: unknown }>();
  readonly #queue: PendingOp[] = [];
  #yieldEvery = 1;
  #opCount = 0;
  /** Append-only audit of applied writes for post-run invariant checks. */
  readonly audit: Array<{ agent: AgentId; key: string; version: number }> = [];

  /** Override how many storage ops pass before the next forced yield. */
  setYieldEvery(value: number): void {
    this.#yieldEvery = Math.max(1, value);
  }

  #gate<T>(agent: AgentId | undefined, op: () => T): Promise<T> {
    if (agent === undefined) {
      return Promise.resolve(op());
    }
    this.#opCount += 1;
    if (this.#opCount % this.#yieldEvery !== 0) {
      return Promise.resolve(op());
    }
    return new Promise<T>((resolve, reject) => {
      this.#queue.push({
        agent,
        run: () => {
          try {
            resolve(op());
          } catch (error) {
            reject(error);
          }
        },
      });
    });
  }

  /** Release one queued operation; returns false when the queue is empty. */
  step(): boolean {
    const pending = this.#queue.shift();
    if (!pending) {
      return false;
    }
    pending.run();
    return true;
  }

  /** Drain the queue in arrival order (the default fair schedule). */
  async drain(): Promise<void> {
    while (this.step()) {
      await Promise.resolve();
    }
  }

  /** Release the next pending op belonging to `agent` (targeted interleaving). */
  stepAgent(agent: AgentId): boolean {
    const index = this.#queue.findIndex((op) => op.agent === agent);
    if (index < 0) {
      return false;
    }
    const [pending] = this.#queue.splice(index, 1);
    pending.run();
    return true;
  }

  pendingAgents(): readonly AgentId[] {
    return this.#queue.map((op) => op.agent);
  }

  async read<T>(key: string): Promise<CasEntry<T | undefined>> {
    const agent = agentStorage.getStore();
    return this.#gate(agent, () => {
      const entry = this.#entries.get(key);
      if (!entry) {
        return { version: 0, value: undefined } as CasEntry<T | undefined>;
      }
      return { version: entry.version, value: entry.value as T };
    });
  }

  async write<T>(key: string, value: T, expectedVersion: number): Promise<number> {
    const agent = agentStorage.getStore();
    return this.#gate(agent, () => {
      const current = this.#entries.get(key);
      const actualVersion = current ? current.version : 0;
      if (actualVersion !== expectedVersion) {
        throw new CasConflict(key, expectedVersion, actualVersion);
      }
      const nextVersion = actualVersion + 1;
      this.#entries.set(key, { version: nextVersion, value });
      this.audit.push({ agent, key, version: nextVersion });
      return nextVersion;
    });
  }

  async delete(key: string, expectedVersion: number): Promise<void> {
    const agent = agentStorage.getStore();
    return this.#gate(agent, () => {
      const current = this.#entries.get(key);
      const actualVersion = current ? current.version : 0;
      if (actualVersion !== expectedVersion) {
        throw new CasConflict(key, expectedVersion, actualVersion);
      }
      this.#entries.delete(key);
      this.audit.push({ agent, key, version: 0 });
    });
  }

  async listKeys(): Promise<readonly string[]> {
    const agent = agentStorage.getStore();
    return this.#gate(agent, () => [...this.#entries.keys()].sort());
  }

  /** Synchronous inspection for test assertions (bypasses the scheduler). */
  inspect(key: string): { version: number; value: unknown } | undefined {
    return this.#entries.get(key);
  }
}

/** Tiny deterministic PRNG so randomized schedule sweeps are reproducible. */
export class DeterministicRng {
  #state: number;

  constructor(seed: number) {
    this.#state = seed >>> 0 || 1;
  }

  next(): number {
    // xorshift32
    let value = this.#state;
    value ^= value << 13;
    value ^= value >>> 17;
    value ^= value << 5;
    this.#state = value >>> 0;
    return this.#state / 0x100000000;
  }

  nextInt(bound: number): number {
    return Math.floor(this.next() * bound);
  }
}
