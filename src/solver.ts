import type {
  CapabilityOffer,
  ConflictReason,
  ConflictRequirement,
  Declaration,
  MinimalConflict,
} from "./types.ts";

interface ConstraintSystem {
  common: Set<string>;
  requires: Map<string, ReadonlySet<string>>;
  mutexPairs: Array<[string, string]>;
  priority: Map<string, number>;
  mandatory: ConflictRequirement[];
  offeredBy: Map<string, string[]>;
}

export type SolveOutcome =
  | { status: "AGREED"; config: string[]; score: number }
  | { status: "CONFLICT"; conflict: MinimalConflict };

function indexOffers(declaration: Declaration): Map<string, CapabilityOffer> {
  const offers = new Map<string, CapabilityOffer>();
  for (const offer of declaration.capabilities) {
    if (offers.has(offer.name)) {
      throw new Error(
        `duplicate capability "${offer.name}" in declaration of party "${declaration.party}"`,
      );
    }
    offers.set(offer.name, offer);
  }
  return offers;
}

function buildSystem(a: Declaration, b: Declaration): ConstraintSystem {
  const offersA = indexOffers(a);
  const offersB = indexOffers(b);

  const offeredBy = new Map<string, string[]>();
  const noteOffer = (name: string, party: string): void => {
    const list = offeredBy.get(name);
    if (list) {
      if (!list.includes(party)) list.push(party);
    } else {
      offeredBy.set(name, [party]);
    }
  };
  for (const name of offersA.keys()) noteOffer(name, a.party);
  for (const name of offersB.keys()) noteOffer(name, b.party);

  const common = new Set<string>();
  for (const name of offersA.keys()) {
    if (offersB.has(name)) common.add(name);
  }

  const requires = new Map<string, ReadonlySet<string>>();
  const priority = new Map<string, number>();
  const mutex = new Set<string>();
  for (const name of common) {
    const offerA = offersA.get(name)!;
    const offerB = offersB.get(name)!;
    requires.set(
      name,
      new Set([...(offerA.requires ?? []), ...(offerB.requires ?? [])]),
    );
    priority.set(name, offerA.priority + offerB.priority);
    for (const offer of [offerA, offerB]) {
      for (const other of offer.conflictsWith ?? []) {
        if (!common.has(other)) continue;
        const pair = name < other ? `${name}|${other}` : `${other}|${name}`;
        mutex.add(pair);
      }
    }
  }
  const mutexPairs: Array<[string, string]> = [...mutex].sort().map((pair) => {
    const [x, y] = pair.split("|");
    return [x!, y!] as [string, string];
  });

  const mandatory: ConflictRequirement[] = [];
  for (const declaration of [a, b]) {
    for (const offer of declaration.capabilities) {
      if (offer.mandatory) {
        mandatory.push({ party: declaration.party, capability: offer.name });
      }
    }
  }

  return { common, requires, mutexPairs, priority, mandatory, offeredBy };
}

function dependencyClosure(
  system: ConstraintSystem,
  roots: Iterable<string>,
): { selected: Set<string>; parent: Map<string, string> } {
  const selected = new Set<string>();
  const parent = new Map<string, string>();
  const queue: string[] = [];
  for (const root of roots) {
    if (!selected.has(root)) {
      selected.add(root);
      queue.push(root);
    }
  }
  while (queue.length > 0) {
    const current = queue.shift()!;
    for (const dep of system.requires.get(current) ?? []) {
      if (!selected.has(dep)) {
        selected.add(dep);
        parent.set(dep, current);
        queue.push(dep);
      }
    }
  }
  return { selected, parent };
}

function isSelectionViable(
  system: ConstraintSystem,
  selected: ReadonlySet<string>,
): boolean {
  for (const name of selected) {
    if (!system.common.has(name)) return false;
  }
  for (const [x, y] of system.mutexPairs) {
    if (selected.has(x) && selected.has(y)) return false;
  }
  return true;
}

function isFeasible(system: ConstraintSystem, roots: string[]): boolean {
  const { selected } = dependencyClosure(system, roots);
  return isSelectionViable(system, selected);
}

function optimize(system: ConstraintSystem): { config: string[]; score: number } {
  const mandatoryNames = system.mandatory.map((m) => m.capability);
  const base = dependencyClosure(system, mandatoryNames).selected;

  let baseScore = 0;
  for (const name of base) baseScore += system.priority.get(name) ?? 0;

  const candidates = [...system.common].filter((name) => !base.has(name)).sort();

  const closureCache = new Map<string, Set<string>>();
  const closureOf = (name: string): Set<string> => {
    let cached = closureCache.get(name);
    if (!cached) {
      cached = dependencyClosure(system, [name]).selected;
      closureCache.set(name, cached);
    }
    return cached;
  };

  let bestSet = new Set(base);
  let bestScore = baseScore;
  let bestKey = [...base].sort().join("|");

  const consider = (selected: Set<string>, score: number): void => {
    const key = [...selected].sort().join("|");
    if (score > bestScore || (score === bestScore && key < bestKey)) {
      bestScore = score;
      bestSet = new Set(selected);
      bestKey = key;
    }
  };

  const search = (index: number, selected: Set<string>, score: number): void => {
    let bound = score;
    for (const candidate of candidates) {
      if (!selected.has(candidate)) {
        bound += Math.max(0, system.priority.get(candidate) ?? 0);
      }
    }
    if (bound < bestScore) return;
    if (index >= candidates.length) {
      consider(selected, score);
      return;
    }
    const name = candidates[index]!;

    let addedScore = 0;
    const added: string[] = [];
    let viable = true;
    for (const member of closureOf(name)) {
      if (selected.has(member)) continue;
      if (!system.common.has(member)) {
        viable = false;
        break;
      }
      added.push(member);
      addedScore += system.priority.get(member) ?? 0;
    }
    if (viable) {
      for (const member of added) selected.add(member);
      if (isSelectionViable(system, selected)) {
        search(index + 1, selected, score + addedScore);
      }
      for (const member of added) selected.delete(member);
    }

    search(index + 1, selected, score);
  };

  search(0, new Set(base), baseScore);
  consider(bestSet, bestScore);

  return { config: [...bestSet].sort(), score: bestScore };
}

function extractMinimalCore(system: ConstraintSystem): ConflictRequirement[] {
  let core = [...system.mandatory];
  for (const candidate of [...core]) {
    const trial = core.filter((item) => item !== candidate);
    if (!isFeasible(system, trial.map((item) => item.capability))) {
      core = trial;
    }
  }
  return core;
}

function explainConflict(
  system: ConstraintSystem,
  core: ConflictRequirement[],
): ConflictReason[] {
  const reasons: ConflictReason[] = [];
  const roots = core.map((item) => item.capability);
  const { selected, parent } = dependencyClosure(system, roots);

  const rootParty = new Map<string, string>();
  for (const item of core) {
    if (!rootParty.has(item.capability)) rootParty.set(item.capability, item.party);
  }

  const chainTo = (name: string): string[] => {
    const chain = [name];
    let cursor = name;
    while (parent.has(cursor)) {
      cursor = parent.get(cursor)!;
      chain.unshift(cursor);
    }
    return chain;
  };

  const rootOf = (name: string): string => {
    let cursor = name;
    while (parent.has(cursor)) cursor = parent.get(cursor)!;
    return cursor;
  };

  const reportedMissing = new Set<string>();
  for (const name of selected) {
    if (system.common.has(name) || reportedMissing.has(name)) continue;
    reportedMissing.add(name);
    const root = rootOf(name);
    reasons.push({
      kind: "missing-offer",
      capability: name,
      offeredBy: system.offeredBy.get(name) ?? [],
      requiredBy: rootParty.get(root) ?? root,
      path: chainTo(name),
    });
  }

  for (const [x, y] of system.mutexPairs) {
    if (selected.has(x) && selected.has(y)) {
      reasons.push({ kind: "mutual-exclusion", capabilities: [x, y] });
    }
  }

  return reasons;
}

export function solveConfiguration(
  declA: Declaration,
  declB: Declaration,
): SolveOutcome {
  const system = buildSystem(declA, declB);
  const mandatoryNames = system.mandatory.map((m) => m.capability);

  if (isFeasible(system, mandatoryNames)) {
    const { config, score } = optimize(system);
    return { status: "AGREED", config, score };
  }

  const core = extractMinimalCore(system);
  return {
    status: "CONFLICT",
    conflict: {
      requirements: core,
      reasons: explainConflict(system, core),
    },
  };
}
