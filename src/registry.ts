import { isDeepStrictEqual } from 'node:util';
import { normalizeEnvelope, type AttributeEnvelope } from './attributes.ts';
import type { AttributeDescriptor } from './condition.ts';
import { combine, type CombinedOutcome, type CombinerName } from './combiners.ts';
import { evaluatePolicy, type Policy } from './policy.ts';

export interface PolicyModule {
  readonly name: string;
  readonly version: number;
  readonly combiner: CombinerName;
  readonly policies: readonly Policy[];
  readonly descriptors: readonly AttributeDescriptor[];
  /** Names of other modules whose attribute descriptors are imported as
   *  interpretation evidence. Cycles ("module rings") are rejected at
   *  activation time. */
  readonly delegates?: readonly string[];
}

export interface DecisionRecord {
  readonly id: number;
  readonly moduleName: string;
  readonly moduleVersion: number;
  readonly envelope: AttributeEnvelope;
  readonly outcome: CombinedOutcome;
}

export type ActivationResult =
  | { readonly ok: true }
  | { readonly ok: false; readonly reason: 'unknown-module' | 'module-ring'; readonly cycle?: readonly string[] };

export interface ReplayResult {
  readonly record: DecisionRecord;
  readonly replayed: CombinedOutcome;
  readonly match: boolean;
}

function findRing(
  active: ReadonlyMap<string, number>,
  modules: ReadonlyMap<string, ReadonlyMap<number, PolicyModule>>,
): string[] | undefined {
  const graph = new Map<string, string[]>();
  for (const [name, version] of active) {
    const module = modules.get(name)?.get(version);
    if (!module) continue;
    graph.set(name, (module.delegates ?? []).filter((dep) => active.has(dep)));
  }
  const state = new Map<string, 'visiting' | 'done'>();
  const stack: string[] = [];
  const visit = (node: string): string[] | undefined => {
    state.set(node, 'visiting');
    stack.push(node);
    for (const next of graph.get(node) ?? []) {
      if (state.get(next) === 'visiting') return [...stack.slice(stack.indexOf(next)), next];
      if (!state.has(next)) {
        const ring = visit(next);
        if (ring) return ring;
      }
    }
    stack.pop();
    state.set(node, 'done');
    return undefined;
  };
  for (const node of graph.keys()) {
    if (!state.has(node)) {
      const ring = visit(node);
      if (ring) return ring;
    }
  }
  return undefined;
}

export class ModuleRegistry {
  readonly #modules = new Map<string, Map<number, PolicyModule>>();
  readonly #active = new Map<string, number>();
  readonly #log: DecisionRecord[] = [];

  register(module: PolicyModule): void {
    let versions = this.#modules.get(module.name);
    if (!versions) {
      versions = new Map();
      this.#modules.set(module.name, versions);
    }
    if (versions.has(module.version)) {
      throw new Error(`module ${module.name}@${module.version} is already registered`);
    }
    versions.set(module.version, module);
  }

  activeVersion(name: string): number | undefined {
    return this.#active.get(name);
  }

  /**
   * Atomic activation: the candidate active set is fully validated first
   * (module exists, delegation graph is acyclic); the active pointer is
   * swapped only after every check has passed. A failed activation leaves
   * the previously active version untouched.
   */
  activate(name: string, version: number): ActivationResult {
    if (!this.#modules.get(name)?.has(version)) {
      return { ok: false, reason: 'unknown-module' };
    }
    const candidate = new Map(this.#active);
    candidate.set(name, version);
    const cycle = findRing(candidate, this.#modules);
    if (cycle) return { ok: false, reason: 'module-ring', cycle };
    this.#active.set(name, version);
    return { ok: true };
  }

  evaluate(name: string, envelope: AttributeEnvelope): DecisionRecord {
    const version = this.#active.get(name);
    if (version === undefined) throw new Error(`module "${name}" has no active version`);
    const module = this.#modules.get(name)!.get(version)!;
    const outcome = this.#evaluateWith(module, envelope);
    const record: DecisionRecord = {
      id: this.#log.length,
      moduleName: name,
      moduleVersion: version,
      envelope,
      outcome,
    };
    this.#log.push(record);
    return record;
  }

  /**
   * Re-evaluates a logged decision with the exact historical module version
   * and the stored envelope. Evaluation is pure (no clock, no I/O, no
   * external identity service), so a faithful replay is bit-identical.
   */
  replay(recordId: number): ReplayResult {
    const record = this.#log[recordId];
    if (!record) throw new Error(`unknown decision record ${recordId}`);
    const module = this.#modules.get(record.moduleName)?.get(record.moduleVersion);
    if (!module) {
      throw new Error(`historical module ${record.moduleName}@${record.moduleVersion} is missing`);
    }
    const replayed = this.#evaluateWith(module, record.envelope);
    return { record, replayed, match: isDeepStrictEqual(record.outcome, replayed) };
  }

  #evaluateWith(module: PolicyModule, envelope: AttributeEnvelope): CombinedOutcome {
    const attributes = normalizeEnvelope(envelope);
    const descriptors = this.#effectiveDescriptors(module);
    const branches = module.policies.map((policy) => evaluatePolicy(policy, attributes, descriptors));
    return combine(module.combiner, branches);
  }

  #effectiveDescriptors(module: PolicyModule): Map<string, AttributeDescriptor> {
    const result = new Map<string, AttributeDescriptor>();
    const visit = (current: PolicyModule, seen: Set<string>): void => {
      if (seen.has(current.name)) return; // rings are rejected at activation; stay total anyway
      seen.add(current.name);
      for (const descriptor of current.descriptors) {
        if (!result.has(descriptor.name)) result.set(descriptor.name, descriptor);
      }
      for (const dep of current.delegates ?? []) {
        const version = this.#active.get(dep);
        const target = version === undefined ? undefined : this.#modules.get(dep)?.get(version);
        if (target) visit(target, seen);
      }
    };
    visit(module, new Set());
    return result;
  }
}
