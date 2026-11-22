import type { PathDFA, PathSegment } from "./types.ts";

export const ANY_SEGMENT = "*";

function uniqueStrings(values: readonly string[]): string[] {
  return [...new Set(values)];
}

export function normalizeDFA(input: PathDFA): PathDFA {
  const states: string[] = [];
  const seen = new Set<string>();
  const addState = (state: string) => {
    if (!seen.has(state)) {
      seen.add(state);
      states.push(state);
    }
  };
  addState(input.start);
  for (const state of input.states) {
    addState(state);
  }
  const transitions: Record<string, Record<string, string>> = {};
  for (const [state, table] of Object.entries(input.transitions)) {
    addState(state);
    const row: Record<string, string> = {};
    for (const [symbol, target] of Object.entries(table)) {
      row[symbol] = target;
      addState(target);
    }
    transitions[state] = row;
  }
  const accepting = uniqueStrings(input.accepting).filter((state) => seen.has(state));
  const alphabet = uniqueStrings(input.alphabet ?? []);
  return {
    states,
    start: input.start,
    accepting,
    transitions,
    alphabet,
  };
}

export function dfaAlphabet(dfa: PathDFA): string[] {
  const symbols = new Set<string>(dfa.alphabet ?? []);
  for (const table of Object.values(dfa.transitions)) {
    for (const symbol of Object.keys(table)) {
      if (symbol !== ANY_SEGMENT) {
        symbols.add(symbol);
      }
    }
  }
  return [...symbols].sort();
}

function rawStep(dfa: PathDFA, state: string, symbol: string): string | undefined {
  const table = dfa.transitions[state];
  if (!table) {
    return undefined;
  }
  return table[symbol] ?? table[ANY_SEGMENT];
}

export function stepDFA(dfa: PathDFA, state: string, symbol: string): string | undefined {
  return rawStep(dfa, state, symbol);
}

export function acceptsPath(dfa: PathDFA, path: readonly PathSegment[]): boolean {
  let state: string | undefined = dfa.start;
  for (const segment of path) {
    state = state === undefined ? undefined : stepDFA(dfa, state, segment);
    if (state === undefined) {
      return false;
    }
  }
  return state !== undefined && dfa.accepting.includes(state);
}

export const dfaAccepts = acceptsPath;

export function completeDFA(input: PathDFA, alphabet = dfaAlphabet(input)): PathDFA {
  const dfa = normalizeDFA(input);
  const symbols = uniqueStrings(alphabet);
  const sink = "__sink__";
  const states = [...dfa.states];
  const transitions: Record<string, Record<string, string>> = {};
  let needsSink = false;

  for (const state of dfa.states) {
    const row: Record<string, string> = {};
    for (const symbol of symbols) {
      const target = rawStep(dfa, state, symbol);
      if (target === undefined) {
        needsSink = true;
        row[symbol] = sink;
      } else {
        row[symbol] = target;
      }
    }
    const catchAll = rawStep(dfa, state, ANY_SEGMENT);
    if (catchAll === undefined) {
      needsSink = true;
      row[ANY_SEGMENT] = sink;
    } else {
      row[ANY_SEGMENT] = catchAll;
    }
    transitions[state] = row;
  }

  if (needsSink || symbols.length > 0) {
    if (!states.includes(sink)) {
      states.push(sink);
    }
    const row: Record<string, string> = {};
    for (const symbol of [...symbols, ANY_SEGMENT]) {
      row[symbol] = sink;
    }
    transitions[sink] = row;
  }

  return {
    states,
    start: dfa.start,
    accepting: dfa.accepting,
    transitions,
    alphabet: symbols,
  };
}

function product(
  leftInput: PathDFA,
  rightInput: PathDFA,
  accepting: (inLeft: boolean, inRight: boolean) => boolean,
): PathDFA {
  const alphabet = uniqueStrings([...dfaAlphabet(leftInput), ...dfaAlphabet(rightInput)]).sort();
  const left = completeDFA(leftInput, alphabet);
  const right = completeDFA(rightInput, alphabet);
  const leftAccepting = new Set(left.accepting);
  const rightAccepting = new Set(right.accepting);
  const pairIds = new Map<string, string>();
  const pairStates: string[] = [];
  const transitions: Record<string, Record<string, string>> = {};
  const acceptingStates: string[] = [];
  const queue: Array<{ left: string; right: string; id: string }> = [];

  const ensure = (leftState: string, rightState: string): string => {
    const key = `${leftState}\u0000${rightState}`;
    const existing = pairIds.get(key);
    if (existing !== undefined) {
      return existing;
    }
    const id = `p${pairIds.size}`;
    pairIds.set(key, id);
    pairStates.push(id);
    queue.push({ left: leftState, right: rightState, id });
    return id;
  };

  ensure(left.start, right.start);
  for (let index = 0; index < queue.length; index += 1) {
    const current = queue[index];
    if (accepting(leftAccepting.has(current.left), rightAccepting.has(current.right))) {
      acceptingStates.push(current.id);
    }
    const row: Record<string, string> = {};
    for (const symbol of [...alphabet, ANY_SEGMENT]) {
      const leftTarget = left.transitions[current.left]?.[symbol] ?? "__sink__";
      const rightTarget = right.transitions[current.right]?.[symbol] ?? "__sink__";
      row[symbol] = ensure(leftTarget, rightTarget);
    }
    transitions[current.id] = row;
  }

  return {
    states: pairStates,
    start: "p0",
    accepting: acceptingStates,
    transitions,
    alphabet,
  };
}

export function intersectDFA(left: PathDFA, right: PathDFA): PathDFA {
  return product(left, right, (a, b) => a && b);
}

export const intersectionDFA = intersectDFA;

export function unionDFA(left: PathDFA, right: PathDFA): PathDFA {
  return product(left, right, (a, b) => a || b);
}

export const unionDFAs = unionDFA;

export function complementDFA(input: PathDFA, alphabet = dfaAlphabet(input)): PathDFA {
  const dfa = completeDFA(input, alphabet);
  const accepting = new Set(dfa.accepting);
  return {
    states: dfa.states,
    start: dfa.start,
    accepting: dfa.states.filter((state) => !accepting.has(state)),
    transitions: dfa.transitions,
    alphabet: dfa.alphabet,
  };
}

export function differenceDFA(left: PathDFA, right: PathDFA): PathDFA {
  return intersectDFA(left, complementDFA(right, uniqueStrings([...dfaAlphabet(left), ...dfaAlphabet(right)])));
}

export const subtractDFA = differenceDFA;

export function isEmptyDFA(dfa: PathDFA): boolean {
  const accepting = new Set(dfa.accepting);
  const visited = new Set<string>();
  const queue = [dfa.start];
  for (let index = 0; index < queue.length; index += 1) {
    const state = queue[index];
    if (visited.has(state)) {
      continue;
    }
    visited.add(state);
    if (accepting.has(state)) {
      return false;
    }
    for (const target of Object.values(dfa.transitions[state] ?? {})) {
      if (!visited.has(target)) {
        queue.push(target);
      }
    }
  }
  return true;
}

export function isSubsetDFA(subset: PathDFA, superset: PathDFA): boolean {
  return isEmptyDFA(differenceDFA(subset, superset));
}

export function areEquivalentDFA(left: PathDFA, right: PathDFA): boolean {
  return isSubsetDFA(left, right) && isSubsetDFA(right, left);
}

export const dfaEquivalent = areEquivalentDFA;

export function areDisjointDFA(left: PathDFA, right: PathDFA): boolean {
  return isEmptyDFA(intersectDFA(left, right));
}

export function minimizeDFA(input: PathDFA, alphabet = dfaAlphabet(input)): PathDFA {
  const dfa = completeDFA(input, alphabet);
  const symbols = [...dfa.alphabet, ANY_SEGMENT];
  const accepting = new Set(dfa.accepting);
  let classes = new Map<string, number>();
  let classCount = 0;
  for (const state of dfa.states) {
    const label = accepting.has(state) ? 1 : 0;
    classes.set(state, label);
    classCount = Math.max(classCount, label + 1);
  }

  for (;;) {
    const signatures = new Map<string, string>();
    const nextClasses = new Map<string, number>();
    const signatureToClass = new Map<string, number>();
    let nextCount = 0;
    for (const state of dfa.states) {
      const signature = [
        accepting.has(state) ? "1" : "0",
        ...symbols.map((symbol) => String(classes.get(dfa.transitions[state]?.[symbol] ?? "__sink__"))),
      ].join("|");
      signatures.set(state, signature);
      if (!signatureToClass.has(signature)) {
        signatureToClass.set(signature, nextCount);
        nextCount += 1;
      }
      nextClasses.set(state, signatureToClass.get(signature) ?? 0);
    }
    classes = nextClasses;
    if (nextCount === classCount) {
      break;
    }
    classCount = nextCount;
  }

  const representative = new Map<number, string>();
  for (const state of dfa.states) {
    const classId = classes.get(state) ?? 0;
    if (!representative.has(classId)) {
      representative.set(classId, state);
    }
  }
  const classIds = [...representative.keys()].sort((a, b) => a - b);
  const idByClass = new Map<number, string>();
  classIds.forEach((classId, index) => idByClass.set(classId, `q${index}`));

  const transitions: Record<string, Record<string, string>> = {};
  const resultAccepting: string[] = [];
  for (const classId of classIds) {
    const state = representative.get(classId) ?? dfa.start;
    const id = idByClass.get(classId) ?? "q0";
    if (accepting.has(state)) {
      resultAccepting.push(id);
    }
    const row: Record<string, string> = {};
    for (const symbol of symbols) {
      const target = dfa.transitions[state]?.[symbol] ?? "__sink__";
      row[symbol] = idByClass.get(classes.get(target) ?? 0) ?? "q0";
    }
    transitions[id] = row;
  }

  return {
    states: classIds.map((classId) => idByClass.get(classId) ?? "q0"),
    start: idByClass.get(classes.get(dfa.start) ?? 0) ?? "q0",
    accepting: resultAccepting,
    transitions,
    alphabet: dfa.alphabet,
  };
}

export function exactPathDFA(path: readonly PathSegment[]): PathDFA {
  const states = path.map((_, index) => `q${index}`).concat(`q${path.length}`);
  const transitions: Record<string, Record<string, string>> = {};
  path.forEach((segment, index) => {
    transitions[`q${index}`] = { [segment]: `q${index + 1}` };
  });
  return {
    states,
    start: "q0",
    accepting: [`q${path.length}`],
    transitions,
    alphabet: uniqueStrings(path),
  };
}

export function pathsDFA(paths: readonly (readonly PathSegment[])[]): PathDFA {
  const transitions: Record<string, Record<string, string>> = {};
  const states = new Set<string>(["q0"]);
  const accepting = new Set<string>();
  const alphabet = new Set<string>();

  const ensure = (id: string) => {
    states.add(id);
    return id;
  };

  for (const path of paths) {
    let state = "q0";
    for (const segment of path) {
      alphabet.add(segment);
      const target = `${state}/${JSON.stringify(segment)}`;
      transitions[state] ??= {};
      transitions[state][segment] = ensure(target);
      state = target;
    }
    accepting.add(state);
  }

  return {
    states: [...states],
    start: "q0",
    accepting: [...accepting],
    transitions,
    alphabet: [...alphabet],
  };
}

export const finitePathsDFA = pathsDFA;

export function emptyDFA(alphabet: readonly string[] = []): PathDFA {
  return {
    states: ["q0"],
    start: "q0",
    accepting: [],
    transitions: {},
    alphabet: uniqueStrings(alphabet),
  };
}

export function universalDFA(alphabet: readonly string[] = []): PathDFA {
  return {
    states: ["q0"],
    start: "q0",
    accepting: ["q0"],
    transitions: { q0: { [ANY_SEGMENT]: "q0" } },
    alphabet: uniqueStrings(alphabet),
  };
}

/**
 * Accepts `prefix` itself and every path that extends it.  The empty prefix is
 * therefore the universal path language.
 */
export function prefixDFA(prefix: readonly PathSegment[]): PathDFA {
  const transitions: Record<string, Record<string, string>> = {};
  prefix.forEach((segment, index) => {
    transitions[`q${index}`] = { [segment]: `q${index + 1}` };
  });
  transitions[`q${prefix.length}`] = { [ANY_SEGMENT]: `q${prefix.length}` };
  return {
    states: Array.from({ length: prefix.length + 1 }, (_, index) => `q${index}`),
    start: "q0",
    accepting: [`q${prefix.length}`],
    transitions,
    alphabet: uniqueStrings(prefix),
  };
}

export function unionManyDFA(automata: readonly PathDFA[]): PathDFA {
  if (automata.length === 0) {
    return emptyDFA();
  }
  return minimizeDFA(automata.slice(1).reduce((result, next) => unionDFA(result, next), automata[0]));
}

export function enumerateDFA(
  input: PathDFA,
  options: { maxDepth?: number; maxWords?: number } = {},
): PathSegment[][] {
  const maxDepth = options.maxDepth ?? Number.POSITIVE_INFINITY;
  const maxWords = options.maxWords ?? 10_000;
  const alphabet = dfaAlphabet(input);
  const accepting = new Set(input.accepting);
  const words: PathSegment[][] = [];
  const seen = new Set<string>();
  const queue: Array<{ state: string; path: PathSegment[] }> = [{ state: input.start, path: [] }];
  for (let index = 0; index < queue.length && words.length < maxWords; index += 1) {
    const current = queue[index];
    const key = `${current.state}\u0000${current.path.join("\u0000")}`;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    if (accepting.has(current.state)) {
      words.push(current.path);
    }
    if (current.path.length >= maxDepth) {
      continue;
    }
    for (const symbol of alphabet) {
      const target = stepDFA(input, current.state, symbol);
      if (target !== undefined) {
        queue.push({ state: target, path: [...current.path, symbol] });
      }
    }
  }
  return words;
}

export function pathToString(path: readonly PathSegment[]): string {
  return `/${path.map((segment) => encodeURIComponent(segment)).join("/")}`;
}

