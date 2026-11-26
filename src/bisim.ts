export interface LabelledTransition {
  readonly label: string;
  readonly to: string;
}

export interface LabelledState {
  readonly id: string;
  readonly observation: string;
  readonly transitions: readonly LabelledTransition[];
}

let classSerial = 0;

export class BisimulationClass {
  readonly id: string;
  readonly observation: string;
  readonly members: readonly string[];
  readonly memberKey: string;

  constructor(observation: string, members: readonly string[]) {
    this.id = `bisim-${classSerial++}`;
    this.observation = observation;
    this.members = Object.freeze([...members]);
    this.memberKey = JSON.stringify(members);
    Object.freeze(this);
  }

  has(stateId: string): boolean {
    return this.members.includes(stateId);
  }

  toString(): string {
    return `${this.id}(${this.observation}:${this.members.join(",")})`;
  }
}

/**
 * Reuses class tokens when the exact member set is unchanged. This is what
 * preserves cached identity across policy-graph revisions that do not touch a
 * particular equivalence class.
 */
export class ClassInterner {
  readonly #tokens = new Map<string, BisimulationClass>();

  intern(observation: string, members: readonly string[]): BisimulationClass {
    const key = JSON.stringify([observation, members]);
    let token = this.#tokens.get(key);
    if (token === undefined) {
      token = new BisimulationClass(observation, members);
      this.#tokens.set(key, token);
    }
    return token;
  }

  get size(): number {
    return this.#tokens.size;
  }
}

export interface BisimulationResult {
  readonly classes: readonly BisimulationClass[];
  readonly classOf: (stateId: string) => BisimulationClass | undefined;
  readonly isBisimilar: (left: string, right: string) => boolean;
  readonly iterationCount: number;
}

function transitionSignature(
  state: LabelledState,
  blockOf: ReadonlyMap<string, number>,
): string {
  const entries: string[] = [];
  for (const transition of state.transitions) {
    const targetBlock = blockOf.get(transition.to);
    if (targetBlock === undefined) {
      throw new Error(`Transition from "${state.id}" targets unknown state "${transition.to}"`);
    }
    entries.push(JSON.stringify([transition.label, targetBlock]));
  }
  entries.sort();
  return JSON.stringify(entries);
}

/**
 * Greatest fixed point / partition refinement for a finite labelled
 * transition system. Two states are bisimilar iff they end in the same stable
 * block. Observations are always part of the signature, so states from
 * different domains or capability levels cannot be merged.
 */
export function computeBisimulation(
  states: readonly LabelledState[],
  interner: ClassInterner = new ClassInterner(),
): BisimulationResult {
  const byId = new Map<string, LabelledState>();
  for (const state of states) {
    if (byId.has(state.id)) {
      throw new Error(`Duplicate state id in bisimulation input: ${state.id}`);
    }
    byId.set(state.id, state);
  }

  const initialGroups = new Map<string, number>();
  const blockOf = new Map<string, number>();
  for (const state of states) {
    let block = initialGroups.get(state.observation);
    if (block === undefined) {
      block = initialGroups.size;
      initialGroups.set(state.observation, block);
    }
    blockOf.set(state.id, block);
  }

  let iterationCount = 0;
  let blockCount = initialGroups.size;
  while (true) {
    iterationCount++;
    const signatures = new Map<string, number>();
    const nextBlockOf = new Map<string, number>();
    for (const state of states) {
      const previous = blockOf.get(state.id) as number;
      const signature = JSON.stringify([
        previous,
        state.observation,
        transitionSignature(state, blockOf),
      ]);
      let next = signatures.get(signature);
      if (next === undefined) {
        next = signatures.size;
        signatures.set(signature, next);
      }
      nextBlockOf.set(state.id, next);
    }
    const nextCount = signatures.size;
    for (const [id, block] of nextBlockOf) blockOf.set(id, block);
    if (nextCount === blockCount) break;
    blockCount = nextCount;
    if (iterationCount > states.length + 2) {
      throw new Error("Bisimulation refinement did not converge");
    }
  }

  const membersByBlock = new Map<number, string[]>();
  for (const [id, block] of blockOf) {
    let members = membersByBlock.get(block);
    if (members === undefined) {
      members = [];
      membersByBlock.set(block, members);
    }
    members.push(id);
  }

  const classes: BisimulationClass[] = [];
  const classByState = new Map<string, BisimulationClass>();
  for (const members of membersByBlock.values()) {
    members.sort();
    const observation = byId.get(members[0])?.observation ?? "";
    const token = interner.intern(observation, members);
    classes.push(token);
    for (const member of members) classByState.set(member, token);
  }

  return {
    classes,
    classOf: (stateId: string) => classByState.get(stateId),
    isBisimilar: (left: string, right: string) => {
      const leftClass = classByState.get(left);
      const rightClass = classByState.get(right);
      return leftClass !== undefined && leftClass === rightClass;
    },
    iterationCount,
  };
}

export interface FiniteStateSpec {
  readonly id: string;
  readonly observation?: string;
  readonly transitions?: readonly LabelledTransition[];
}

/**
 * The relational finite-state model used by the tests and by
 * `host.policyModel()`. It is intentionally small: observation labels plus
 * labelled edges are enough to compare round trips and revocation partitions.
 */
export class FiniteStateModel {
  readonly states: readonly LabelledState[];
  readonly #interner = new ClassInterner();

  constructor(states: readonly FiniteStateSpec[]) {
    this.states = Object.freeze(
      states.map((state) =>
        Object.freeze({
          id: state.id,
          observation: state.observation ?? "",
          transitions: Object.freeze(
            [...(state.transitions ?? [])].map((transition) =>
              Object.freeze({
                label: transition.label,
                to: transition.to,
              }),
            ),
          ),
        }),
      ),
    );
  }

  bisimulation(interner?: ClassInterner): BisimulationResult {
    return computeBisimulation(this.states, interner ?? this.#interner);
  }

  state(id: string): LabelledState {
    const state = this.states.find((candidate) => candidate.id === id);
    if (state === undefined) throw new Error(`Unknown model state: ${id}`);
    return state;
  }

  reachableFrom(start: string | readonly string[]): Set<string> {
    const starts = typeof start === "string" ? [start] : start;
    const seen = new Set<string>();
    const queue = [...starts];
    while (queue.length > 0) {
      const id = queue.shift() as string;
      if (seen.has(id)) continue;
      const state = this.state(id);
      seen.add(id);
      for (const transition of state.transitions) {
        if (!seen.has(transition.to)) queue.push(transition.to);
      }
    }
    return seen;
  }

  hasPath(
    from: string,
    to: string,
    label?: string,
    allowed: ReadonlySet<string> = this.reachableFrom(from),
  ): boolean {
    if (from === to && label === undefined) return true;
    const seen = new Set<string>();
    const queue = [from];
    while (queue.length > 0) {
      const id = queue.shift() as string;
      if (seen.has(id)) continue;
      seen.add(id);
      for (const transition of this.state(id).transitions) {
        if (label !== undefined && transition.label !== label) continue;
        if (!allowed.has(transition.to)) continue;
        if (transition.to === to) return true;
        queue.push(transition.to);
      }
    }
    return false;
  }
}

export function bisimilarFiniteStates(
  states: readonly FiniteStateSpec[],
  left: string,
  right: string,
): boolean {
  return new FiniteStateModel(states).bisimulation().isBisimilar(left, right);
}
