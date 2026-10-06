import { fnv1a32 } from './hash';
import { FlagConfig } from './types';

export class DependencyCycleError extends Error {
  readonly cycle: string[];
  constructor(cycle: string[]) {
    super(`dependency cycle detected: ${cycle.join(' -> ')}`);
    this.name = 'DependencyCycleError';
    this.cycle = cycle;
  }
}

export class ConfigValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ConfigValidationError';
  }
}

function deepCopy<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

/**
 * Validates a full flag config set. Throws on duplicate ids, dangling
 * dependency references, or dependency cycles. Pure: touches no state.
 */
export function validateConfigs(flags: FlagConfig[]): void {
  const ids = new Set<string>();
  for (const flag of flags) {
    if (ids.has(flag.id)) {
      throw new ConfigValidationError(`duplicate flag id: ${flag.id}`);
    }
    ids.add(flag.id);
  }
  for (const flag of flags) {
    for (const dep of flag.dependencies ?? []) {
      if (!ids.has(dep.flagId)) {
        throw new ConfigValidationError(
          `flag ${flag.id} depends on unknown flag: ${dep.flagId}`,
        );
      }
    }
  }
  // Iterative DFS, three colors, to report the actual cycle path.
  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Map<string, number>();
  const byId = new Map(flags.map((f) => [f.id, f]));
  for (const flag of flags) {
    if (color.get(flag.id) !== undefined) continue;
    const stack: Array<{ id: string; depIndex: number }> = [{ id: flag.id, depIndex: 0 }];
    color.set(flag.id, GRAY);
    while (stack.length > 0) {
      const top = stack[stack.length - 1];
      const deps = byId.get(top.id)!.dependencies ?? [];
      if (top.depIndex >= deps.length) {
        color.set(top.id, BLACK);
        stack.pop();
        continue;
      }
      const depId = deps[top.depIndex].flagId;
      top.depIndex++;
      const depColor = color.get(depId) ?? WHITE;
      if (depColor === GRAY) {
        const cycleStart = stack.findIndex((frame) => frame.id === depId);
        const cycle = stack.slice(cycleStart).map((frame) => frame.id);
        cycle.push(depId);
        throw new DependencyCycleError(cycle);
      }
      if (depColor === WHITE) {
        color.set(depId, GRAY);
        stack.push({ id: depId, depIndex: 0 });
      }
    }
  }
}

/**
 * An immutable, versioned view of a full flag config set. Bucketing
 * parameters (rollout percentage, salt) live here, so changing them in a
 * later update never alters evaluation against an older snapshot.
 */
export class Snapshot {
  readonly flags: ReadonlyMap<string, FlagConfig>;
  private readonly closureDigests = new Map<string, string>();
  private readonly configDigests = new Map<string, string>();

  constructor(readonly version: number, flags: FlagConfig[]) {
    const copied = deepCopy(flags);
    this.flags = new Map(copied.map((f) => [f.id, Object.freeze(f) as FlagConfig]));
  }

  private configDigest(flagId: string): string {
    let digest = this.configDigests.get(flagId);
    if (digest === undefined) {
      digest = fnv1a32(JSON.stringify(this.flags.get(flagId))).toString(16);
      this.configDigests.set(flagId, digest);
    }
    return digest;
  }

  /**
   * Digest of the transitive dependency closure of `flagId` within this
   * snapshot: which flags it (transitively) depends on, plus each of their
   * config digests. Any change to a flag in the closure changes the digest.
   */
  closureDigest(flagId: string): string {
    const cached = this.closureDigests.get(flagId);
    if (cached !== undefined) return cached;
    const closure = new Set<string>();
    const walk = (id: string): void => {
      const config = this.flags.get(id);
      if (config === undefined) return;
      for (const dep of config.dependencies ?? []) {
        if (!closure.has(dep.flagId)) {
          closure.add(dep.flagId);
          walk(dep.flagId);
        }
      }
    };
    walk(flagId);
    const material = Array.from(closure)
      .sort()
      .map((id) => `${id}:${this.configDigest(id)}`)
      .join('|');
    const digest = `${closure.size}:${fnv1a32(material).toString(16)}`;
    this.closureDigests.set(flagId, digest);
    return digest;
  }
}
