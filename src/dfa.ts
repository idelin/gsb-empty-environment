import { stableDigest } from "./util.ts";

export const DFA_ALPHABET_ID = "ascii-0-127+other" as const;
export const DFA_ALPHABET_SIZE = 129;
export const OTHER_INDEX = 128;
export const OTHER_CHAR = "\u{10FFFF}";

const ALL_MASK = (1n << BigInt(DFA_ALPHABET_SIZE)) - 1n;

export interface DFA {
  version: 1;
  alphabet: typeof DFA_ALPHABET_ID;
  start: number;
  accepting: boolean[];
  transitions: number[][];
}

export interface SerializedDfa {
  version: 1;
  alphabet: typeof DFA_ALPHABET_ID;
  start: number;
  accepting: boolean[];
  transitions: number[][];
}

export type RegexLanguageSpec =
  | string
  | RegExp
  | { regex: string; flags?: string }
  | { pattern: string; flags?: string };

export type DomainLanguageSpec =
  | RegexLanguageSpec
  | DFA
  | SerializedDfa
  | { dfa: DFA | SerializedDfa };

export interface CompileLanguageOptions {
  ignoreCase?: boolean;
}

export function charIndex(char: string): number {
  if (char.length === 0) return OTHER_INDEX;
  const code = char.codePointAt(0) ?? OTHER_INDEX;
  return code >= 0 && code < 128 ? code : OTHER_INDEX;
}

export function indexChar(index: number): string {
  if (index === OTHER_INDEX) return OTHER_CHAR;
  return String.fromCharCode(index);
}

function bit(index: number): bigint {
  return 1n << BigInt(index);
}

function literalBit(char: string, ignoreCase: boolean): bigint {
  let normalized = char;
  if (ignoreCase) {
    const lower = char.toLowerCase();
    if (lower.length === 1) normalized = lower;
  }
  return bit(charIndex(normalized));
}

function rangeBits(from: string, to: string, ignoreCase: boolean): bigint {
  let left = from;
  let right = to;
  if (ignoreCase) {
    const lowerLeft = from.toLowerCase();
    const lowerRight = to.toLowerCase();
    if (lowerLeft.length === 1) left = lowerLeft;
    if (lowerRight.length === 1) right = lowerRight;
  }
  const a = charIndex(left);
  const b = charIndex(right);
  let mask = 0n;
  const low = Math.min(a, b);
  const high = Math.max(a, b);
  for (let index = low; index <= high; index += 1) mask |= bit(index);
  return mask;
}

function digitMask(): bigint {
  return rangeBits("0", "9", false);
}

function wordMask(): bigint {
  return rangeBits("a", "z", false) | rangeBits("0", "9", false) | bit("_".charCodeAt(0));
}

function spaceMask(): bigint {
  return (
    bit(" ".charCodeAt(0)) |
    bit("\t".charCodeAt(0)) |
    bit("\n".charCodeAt(0)) |
    bit("\r".charCodeAt(0)) |
    bit("\f".charCodeAt(0))
  );
}

type RegexNode =
  | { kind: "empty" }
  | { kind: "chars"; mask: bigint }
  | { kind: "concat"; parts: RegexNode[] }
  | { kind: "alt"; options: RegexNode[] }
  | { kind: "star"; child: RegexNode }
  | { kind: "plus"; child: RegexNode }
  | { kind: "optional"; child: RegexNode }
  | { kind: "repeat"; child: RegexNode; min: number; max: number | null };

interface ParserState {
  source: string;
  pos: number;
  ignoreCase: boolean;
}

class RegexSyntaxError extends Error {
  constructor(message: string, state: ParserState) {
    super(`${message} at offset ${state.pos} of ${JSON.stringify(state.source)}`);
    this.name = "RegexSyntaxError";
  }
}

function parseRegexExpression(source: string, ignoreCase: boolean): RegexNode {
  const state: ParserState = { source, pos: 0, ignoreCase };
  const node = parseAlternation(state);
  if (state.pos !== source.length) {
    throw new RegexSyntaxError("unexpected trailing input", state);
  }
  return node;
}

function peek(state: ParserState): string {
  return state.source[state.pos] ?? "";
}

function consume(state: ParserState): string {
  const char = peek(state);
  if (char !== "") state.pos += 1;
  return char;
}

function parseAlternation(state: ParserState): RegexNode {
  const options = [parseConcatenation(state)];
  while (peek(state) === "|") {
    state.pos += 1;
    options.push(parseConcatenation(state));
  }
  return options.length === 1 ? options[0] : { kind: "alt", options };
}

function parseConcatenation(state: ParserState): RegexNode {
  const parts: RegexNode[] = [];
  while (state.pos < state.source.length) {
    const char = peek(state);
    if (char === "|" || char === ")") break;
    parts.push(parseRepetition(state));
  }
  if (parts.length === 0) return { kind: "empty" };
  if (parts.length === 1) return parts[0];
  return { kind: "concat", parts };
}

function parseRepetition(state: ParserState): RegexNode {
  let node = parseAtom(state);
  while (true) {
    const char = peek(state);
    if (char === "*") {
      state.pos += 1;
      node = { kind: "star", child: node };
      continue;
    }
    if (char === "+") {
      state.pos += 1;
      node = { kind: "plus", child: node };
      continue;
    }
    if (char === "?") {
      state.pos += 1;
      node = { kind: "optional", child: node };
      continue;
    }
    if (char === "{") {
      const bounds = tryParseBounds(state);
      if (bounds) {
        node = {
          kind: "repeat",
          child: node,
          min: bounds.min,
          max: bounds.max,
        };
        continue;
      }
    }
    break;
  }
  return node;
}

function tryParseBounds(state: ParserState): { min: number; max: number | null } | null {
  const rest = state.source.slice(state.pos);
  const match = /^\{(\d+)(?:,(\d*))?\}/.exec(rest);
  if (!match) return null;
  const min = Number(match[1]);
  const second = match[2];
  const max = second === undefined ? min : second === "" ? null : Number(second);
  if (max !== null && max < min) {
    throw new RegexSyntaxError("repetition maximum is smaller than minimum", state);
  }
  if (min > 256 || (max !== null && max > 256)) {
    throw new RegexSyntaxError("repetition bound is too large", state);
  }
  state.pos += match[0].length;
  return { min, max };
}

function parseAtom(state: ParserState): RegexNode {
  const char = peek(state);
  if (char === "") throw new RegexSyntaxError("expected an expression", state);
  if (char === "(") {
    state.pos += 1;
    if (peek(state) === "?" && state.source[state.pos + 1] === ":") {
      state.pos += 2;
    } else if (peek(state) === "?") {
      throw new RegexSyntaxError("lookaround groups are not supported", state);
    }
    const inner = parseAlternation(state);
    if (peek(state) !== ")") throw new RegexSyntaxError("missing ')'", state);
    state.pos += 1;
    return inner;
  }
  if (char === "[") return parseCharacterClass(state);
  if (char === ".") {
    state.pos += 1;
    return { kind: "chars", mask: ALL_MASK };
  }
  if (char === "\\") return { kind: "chars", mask: parseEscape(state).mask };
  if (char === "^" || char === "$") {
    // The matcher always consumes the whole domain, so anchors are implicit.
    state.pos += 1;
    return { kind: "empty" };
  }
  if (char === "*" || char === "+" || char === "?" || char === ")") {
    throw new RegexSyntaxError(`unexpected ${char}`, state);
  }
  state.pos += 1;
  return { kind: "chars", mask: literalBit(char, state.ignoreCase) };
}

interface EscapeResult {
  mask: bigint;
  single: string | null;
}

function parseEscape(state: ParserState): EscapeResult {
  if (consume(state) !== "\\") throw new RegexSyntaxError("expected escape", state);
  const char = consume(state);
  if (char === "") throw new RegexSyntaxError("trailing backslash", state);
  if (char === "d") return { mask: digitMask(), single: null };
  if (char === "D") return { mask: ALL_MASK ^ digitMask(), single: null };
  if (char === "w") return { mask: wordMask(), single: null };
  if (char === "W") return { mask: ALL_MASK ^ wordMask(), single: null };
  if (char === "s") return { mask: spaceMask(), single: null };
  if (char === "S") return { mask: ALL_MASK ^ spaceMask(), single: null };
  if (char === "n") return { mask: bit("\n".charCodeAt(0)), single: "\n" };
  if (char === "r") return { mask: bit("\r".charCodeAt(0)), single: "\r" };
  if (char === "t") return { mask: bit("\t".charCodeAt(0)), single: "\t" };
  if (char === "x") {
    const hex = state.source.slice(state.pos, state.pos + 2);
    if (!/^[0-9a-fA-F]{2}$/.test(hex)) {
      throw new RegexSyntaxError("invalid \\x escape", state);
    }
    state.pos += 2;
    const literal = String.fromCharCode(Number.parseInt(hex, 16));
    return { mask: literalBit(literal, state.ignoreCase), single: literal };
  }
  if (char === "u") {
    const hex = state.source.slice(state.pos, state.pos + 4);
    if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
      throw new RegexSyntaxError("invalid \\u escape", state);
    }
    state.pos += 4;
    const literal = String.fromCharCode(Number.parseInt(hex, 16));
    return { mask: literalBit(literal, state.ignoreCase), single: literal };
  }
  return { mask: literalBit(char, state.ignoreCase), single: char };
}

function parseCharacterClass(state: ParserState): RegexNode {
  consume(state); // [
  let negated = false;
  if (peek(state) === "^") {
    negated = true;
    state.pos += 1;
  }
  let mask = 0n;
  let first = true;
  while (state.pos < state.source.length) {
    if (peek(state) === "]" && !first) break;
    const left = parseClassUnit(state);
    first = false;
    if (
      left.single !== null &&
      peek(state) === "-" &&
      state.source[state.pos + 1] !== "]" &&
      state.source[state.pos + 1] !== undefined
    ) {
      state.pos += 1;
      const right = parseClassUnit(state);
      if (right.single === null) {
        throw new RegexSyntaxError("invalid character-class range", state);
      }
      mask |= rangeBits(left.single, right.single, state.ignoreCase);
    } else {
      mask |= left.mask;
    }
  }
  if (peek(state) !== "]") throw new RegexSyntaxError("unterminated character class", state);
  state.pos += 1;
  return { kind: "chars", mask: negated ? ALL_MASK ^ mask : mask };
}

function parseClassUnit(state: ParserState): EscapeResult {
  const char = consume(state);
  if (char === "") throw new RegexSyntaxError("unterminated character class", state);
  if (char === "\\") {
    state.pos -= 1;
    return parseEscape(state);
  }
  return { mask: literalBit(char, state.ignoreCase), single: char };
}

interface NFAState {
  epsilon: number[];
  moves: Array<{ mask: bigint; to: number }>;
}

interface NFA {
  states: NFAState[];
}

function newNfaState(nfa: NFA): number {
  nfa.states.push({ epsilon: [], moves: [] });
  return nfa.states.length - 1;
}

function addEpsilon(nfa: NFA, from: number, to: number): void {
  nfa.states[from].epsilon.push(to);
}

function buildNfaNode(nfa: NFA, node: RegexNode): { start: number; end: number } {
  if (node.kind === "empty") {
    const start = newNfaState(nfa);
    const end = newNfaState(nfa);
    addEpsilon(nfa, start, end);
    return { start, end };
  }
  if (node.kind === "chars") {
    const start = newNfaState(nfa);
    const end = newNfaState(nfa);
    nfa.states[start].moves.push({ mask: node.mask, to: end });
    return { start, end };
  }
  if (node.kind === "concat") {
    if (node.parts.length === 0) return buildNfaNode(nfa, { kind: "empty" });
    let first = buildNfaNode(nfa, node.parts[0]);
    const start = first.start;
    for (let index = 1; index < node.parts.length; index += 1) {
      const next = buildNfaNode(nfa, node.parts[index]);
      addEpsilon(nfa, first.end, next.start);
      first = { start, end: next.end };
    }
    return first;
  }
  if (node.kind === "alt") {
    const start = newNfaState(nfa);
    const end = newNfaState(nfa);
    for (const option of node.options) {
      const branch = buildNfaNode(nfa, option);
      addEpsilon(nfa, start, branch.start);
      addEpsilon(nfa, branch.end, end);
    }
    return { start, end };
  }
  if (node.kind === "star") {
    const start = newNfaState(nfa);
    const end = newNfaState(nfa);
    const child = buildNfaNode(nfa, node.child);
    addEpsilon(nfa, start, child.start);
    addEpsilon(nfa, start, end);
    addEpsilon(nfa, child.end, child.start);
    addEpsilon(nfa, child.end, end);
    return { start, end };
  }
  if (node.kind === "plus") {
    const child = buildNfaNode(nfa, node.child);
    const star = buildNfaNode(nfa, { kind: "star", child: node.child });
    addEpsilon(nfa, child.end, star.start);
    return { start: child.start, end: star.end };
  }
  if (node.kind === "optional") {
    const start = newNfaState(nfa);
    const end = newNfaState(nfa);
    const child = buildNfaNode(nfa, node.child);
    addEpsilon(nfa, start, child.start);
    addEpsilon(nfa, start, end);
    addEpsilon(nfa, child.end, end);
    return { start, end };
  }
  if (node.kind === "repeat") {
    const parts: RegexNode[] = [];
    for (let index = 0; index < node.min; index += 1) parts.push(node.child);
    if (node.max === null) {
      parts.push({ kind: "star", child: node.child });
    } else {
      for (let index = node.min; index < node.max; index += 1) {
        parts.push({ kind: "optional", child: node.child });
      }
    }
    return buildNfaNode(nfa, parts.length === 0 ? { kind: "empty" } : { kind: "concat", parts });
  }
  const neverNode: never = node;
  throw new Error(`unhandled regex node: ${String(neverNode)}`);
}

function epsilonClosure(nfa: NFA, seeds: Iterable<number>): number[] {
  const seen = new Uint8Array(nfa.states.length);
  const stack = [...seeds];
  const result: number[] = [];
  while (stack.length > 0) {
    const state = stack.pop() as number;
    if (seen[state]) continue;
    seen[state] = 1;
    result.push(state);
    for (const next of nfa.states[state].epsilon) stack.push(next);
  }
  result.sort((a, b) => a - b);
  return result;
}

function determinize(nfa: NFA, start: number, end: number): DFA {
  const initial = epsilonClosure(nfa, [start]);
  const stateSets: number[][] = [initial];
  const stateIds = new Map<string, number>();
  stateIds.set(initial.join(","), 0);
  const accepting: boolean[] = [initial.includes(end)];
  const transitions: number[][] = [];
  let cursor = 0;

  while (cursor < stateSets.length) {
    if (stateSets.length > 10000) {
      throw new Error("regular expression produced too many DFA states");
    }
    const set = stateSets[cursor];
    const row = new Array<number>(DFA_ALPHABET_SIZE).fill(-1);
    for (let symbol = 0; symbol < DFA_ALPHABET_SIZE; symbol += 1) {
      const symbolBit = bit(symbol);
      const seeds: number[] = [];
      for (const nfaState of set) {
        for (const move of nfa.states[nfaState].moves) {
          if ((move.mask & symbolBit) !== 0n) seeds.push(move.to);
        }
      }
      const next = epsilonClosure(nfa, seeds);
      if (next.length === 0) continue;
      const key = next.join(",");
      let id = stateIds.get(key);
      if (id === undefined) {
        id = stateSets.length;
        stateIds.set(key, id);
        stateSets.push(next);
        accepting.push(next.includes(end));
      }
      row[symbol] = id;
    }
    transitions.push(row);
    cursor += 1;
  }

  return {
    version: 1,
    alphabet: DFA_ALPHABET_ID,
    start: 0,
    accepting,
    transitions,
  };
}

export function compileRegex(pattern: string, options: CompileLanguageOptions = {}): DFA {
  const ignoreCase = options.ignoreCase ?? true;
  const node = parseRegexExpression(pattern, ignoreCase);
  const nfa: NFA = { states: [] };
  const built = buildNfaNode(nfa, node);
  return determinize(nfa, built.start, built.end);
}

const regexCache = new Map<string, DFA>();

function compileRegexCached(pattern: string, options: CompileLanguageOptions): DFA {
  const ignoreCase = options.ignoreCase ?? true;
  const key = `${ignoreCase ? "i" : "s"}\u0000${pattern}`;
  const cached = regexCache.get(key);
  if (cached) return cached;
  const compiled = compileRegex(pattern, { ignoreCase });
  regexCache.set(key, compiled);
  return compiled;
}

function isDfaLike(value: unknown): value is DFA {
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return (
    record.version === 1 &&
    record.alphabet === DFA_ALPHABET_ID &&
    typeof record.start === "number" &&
    Array.isArray(record.accepting) &&
    Array.isArray(record.transitions)
  );
}

export function compileDomainLanguage(
  spec: DomainLanguageSpec,
  options: CompileLanguageOptions = {},
): DFA {
  if (typeof spec === "string") return compileRegexCached(spec, options);
  if (spec instanceof RegExp) {
    return compileRegexCached(spec.source, {
      ignoreCase: options.ignoreCase ?? spec.flags.includes("i"),
    });
  }
  if (isDfaLike(spec)) return sanitizeDfa(spec);
  if (spec && typeof spec === "object") {
    const record = spec as Record<string, unknown>;
    if (record.dfa !== undefined) return compileDomainLanguage(record.dfa as DomainLanguageSpec, options);
    if (typeof record.regex === "string") {
      return compileRegexCached(record.regex, {
        ignoreCase: options.ignoreCase ?? (typeof record.flags === "string" ? record.flags.includes("i") : true),
      });
    }
    if (typeof record.pattern === "string") {
      return compileRegexCached(record.pattern, {
        ignoreCase: options.ignoreCase ?? (typeof record.flags === "string" ? record.flags.includes("i") : true),
      });
    }
  }
  throw new TypeError("unsupported domain language specification");
}

export function sanitizeDfa(dfa: DFA): DFA {
  if (!isDfaLike(dfa)) throw new TypeError("invalid DFA");
  const stateCount = dfa.accepting.length;
  if (!Number.isInteger(dfa.start) || dfa.start < 0 || dfa.start >= stateCount) {
    throw new TypeError("invalid DFA start state");
  }
  if (dfa.transitions.length !== stateCount) {
    throw new TypeError("DFA transition table does not match accepting table");
  }
  const transitions = dfa.transitions.map((row) => {
    if (!Array.isArray(row) || row.length !== DFA_ALPHABET_SIZE) {
      throw new TypeError("DFA transition row has the wrong width");
    }
    return row.map((target) => {
      if (!Number.isInteger(target) || target < -1 || target >= stateCount) {
        throw new TypeError("DFA transition target is invalid");
      }
      return target;
    });
  });
  return {
    version: 1,
    alphabet: DFA_ALPHABET_ID,
    start: dfa.start,
    accepting: dfa.accepting.map(Boolean),
    transitions,
  };
}

export function serializeDfa(dfa: DFA): SerializedDfa {
  const clean = sanitizeDfa(dfa);
  return {
    version: 1,
    alphabet: DFA_ALPHABET_ID,
    start: clean.start,
    accepting: [...clean.accepting],
    transitions: clean.transitions.map((row) => [...row]),
  };
}

export function deserializeDfa(value: SerializedDfa | DFA): DFA {
  return sanitizeDfa(value);
}

export function dfaAccepts(dfa: DFA, text: string): boolean {
  const clean = sanitizeDfa(dfa);
  let state = clean.start;
  for (const char of text) {
    const next = clean.transitions[state][charIndex(char)];
    if (next < 0) return false;
    state = next;
  }
  return clean.accepting[state];
}

export function dfaIsEmpty(dfa: DFA): boolean {
  const clean = sanitizeDfa(dfa);
  const queue = [clean.start];
  const seen = new Set<number>([clean.start]);
  while (queue.length > 0) {
    const state = queue.shift() as number;
    if (clean.accepting[state]) return false;
    const row = clean.transitions[state];
    for (const next of row) {
      if (next >= 0 && !seen.has(next)) {
        seen.add(next);
        queue.push(next);
      }
    }
  }
  return true;
}

const SAMPLE_ORDER: number[] = (() => {
  const order: number[] = [];
  const push = (char: string) => {
    const index = charIndex(char);
    if (!order.includes(index)) order.push(index);
  };
  for (const char of "abcdefghijklmnopqrstuvwxyz") push(char);
  for (const char of "0123456789") push(char);
  for (const char of ".-_@") push(char);
  for (let index = 32; index < 127; index += 1) push(String.fromCharCode(index));
  for (let index = 0; index < 32; index += 1) push(String.fromCharCode(index));
  push(OTHER_CHAR);
  return order;
})();

export function dfaSample(dfa: DFA): string | null {
  const clean = sanitizeDfa(dfa);
  if (dfaIsEmpty(clean)) return null;
  if (clean.accepting[clean.start]) return "";
  const queue: Array<{ state: number; text: string }> = [{ state: clean.start, text: "" }];
  const seen = new Set<number>([clean.start]);
  let cursor = 0;
  while (cursor < queue.length) {
    const current = queue[cursor];
    cursor += 1;
    for (const symbol of SAMPLE_ORDER) {
      const next = clean.transitions[current.state][symbol];
      if (next < 0 || seen.has(next)) continue;
      const text = current.text + indexChar(symbol);
      if (clean.accepting[next]) return text;
      seen.add(next);
      queue.push({ state: next, text });
    }
  }
  return null;
}

function completeDfa(dfa: DFA): DFA {
  const clean = sanitizeDfa(dfa);
  let dead = -1;
  const transitions = clean.transitions.map((row) => {
    const copy = [...row];
    for (let index = 0; index < copy.length; index += 1) {
      if (copy[index] < 0) {
        if (dead < 0) dead = clean.accepting.length;
        copy[index] = dead;
      }
    }
    return copy;
  });
  if (dead < 0) {
    return { ...clean, transitions: transitions.map((row) => [...row]), accepting: [...clean.accepting] };
  }
  const deadRow = new Array<number>(DFA_ALPHABET_SIZE).fill(dead);
  transitions.push(deadRow);
  return {
    ...clean,
    accepting: [...clean.accepting, false],
    transitions,
  };
}

function relabelBfs(dfa: DFA): DFA {
  const clean = sanitizeDfa(dfa);
  const order: number[] = [];
  const ids = new Map<number, number>();
  const queue = [clean.start];
  ids.set(clean.start, 0);
  while (queue.length > 0) {
    const state = queue.shift() as number;
    order.push(state);
    for (let symbol = 0; symbol < DFA_ALPHABET_SIZE; symbol += 1) {
      const next = clean.transitions[state][symbol];
      if (next >= 0 && !ids.has(next)) {
        ids.set(next, ids.size);
        queue.push(next);
      }
    }
  }
  const transitions = order.map((state) =>
    clean.transitions[state].map((next) => (next < 0 ? -1 : (ids.get(next) as number))),
  );
  const accepting = order.map((state) => clean.accepting[state]);
  return {
    version: 1,
    alphabet: DFA_ALPHABET_ID,
    start: 0,
    accepting,
    transitions,
  };
}

export function minimizeDfa(dfa: DFA): DFA {
  const complete = completeDfa(dfa);
  const stateCount = complete.accepting.length;
  const blocks = new Array<number>(stateCount).fill(0);
  const hasAccepting = complete.accepting.some(Boolean);
  for (let state = 0; state < stateCount; state += 1) {
    blocks[state] = complete.accepting[state] ? 1 : 0;
  }
  let blockCount = hasAccepting ? 2 : 1;

  for (;;) {
    const signatures = new Map<string, number>();
    const nextBlocks = new Array<number>(stateCount).fill(0);
    for (let state = 0; state < stateCount; state += 1) {
      const targets = complete.transitions[state].map((target) => blocks[target]).join(",");
      const signature = `${blocks[state]}|${targets}`;
      let block = signatures.get(signature);
      if (block === undefined) {
        block = signatures.size;
        signatures.set(signature, block);
      }
      nextBlocks[state] = block;
    }
    const nextBlockCount = signatures.size;
    let stable = nextBlockCount === blockCount;
    if (stable) {
      for (let state = 0; state < stateCount; state += 1) {
        if (nextBlocks[state] !== blocks[state]) {
          stable = false;
          break;
        }
      }
    }
    blocks.splice(0, blocks.length, ...nextBlocks);
    blockCount = nextBlockCount;
    if (stable) break;
  }

  const representative = new Map<number, number>();
  for (let state = 0; state < stateCount; state += 1) {
    if (!representative.has(blocks[state])) representative.set(blocks[state], state);
  }
  const blockIds = [...representative.keys()].sort((a, b) => a - b);
  const renumber = new Map<number, number>();
  blockIds.forEach((block, index) => renumber.set(block, index));
  const transitions = blockIds.map((block) => {
    const state = representative.get(block) as number;
    return complete.transitions[state].map((target) => renumber.get(blocks[target]) as number);
  });
  const accepting = blockIds.map((block) => complete.accepting[representative.get(block) as number]);
  return relabelBfs({
    version: 1,
    alphabet: DFA_ALPHABET_ID,
    start: renumber.get(blocks[complete.start]) as number,
    accepting,
    transitions,
  });
}

function productDfa(a: DFA, b: DFA, mode: "intersection" | "union"): DFA {
  const left = sanitizeDfa(a);
  const right = sanitizeDfa(b);
  const pairs: Array<[number, number]> = [[left.start, right.start]];
  const ids = new Map<string, number>([["0:0", 0]]);
  const keys = [`${left.start}:${right.start}`];
  ids.set(keys[0], 0);
  const accepting: boolean[] = [];
  const transitions: number[][] = [];
  let cursor = 0;

  while (cursor < pairs.length) {
    const [leftState, rightState] = pairs[cursor];
    const leftAccepted = leftState >= 0 && left.accepting[leftState];
    const rightAccepted = rightState >= 0 && right.accepting[rightState];
    accepting.push(mode === "intersection" ? leftAccepted && rightAccepted : leftAccepted || rightAccepted);
    const row = new Array<number>(DFA_ALPHABET_SIZE).fill(-1);
    for (let symbol = 0; symbol < DFA_ALPHABET_SIZE; symbol += 1) {
      const nextLeft = leftState >= 0 ? left.transitions[leftState][symbol] : -1;
      const nextRight = rightState >= 0 ? right.transitions[rightState][symbol] : -1;
      let nextLeftOut = nextLeft;
      let nextRightOut = nextRight;
      if (mode === "intersection") {
        if (nextLeft < 0 || nextRight < 0) {
          nextLeftOut = -1;
          nextRightOut = -1;
        }
      } else if (nextLeft < 0 && nextRight < 0) {
        nextLeftOut = -1;
        nextRightOut = -1;
      }
      if (nextLeftOut < 0 && nextRightOut < 0) continue;
      const key = `${nextLeftOut}:${nextRightOut}`;
      let id = ids.get(key);
      if (id === undefined) {
        id = pairs.length;
        ids.set(key, id);
        pairs.push([nextLeftOut, nextRightOut]);
      }
      row[symbol] = id;
    }
    transitions.push(row);
    cursor += 1;
  }

  return {
    version: 1,
    alphabet: DFA_ALPHABET_ID,
    start: 0,
    accepting,
    transitions,
  };
}

export function dfaIntersection(a: DFA, b: DFA): DFA {
  return productDfa(a, b, "intersection");
}

export function dfaUnion(a: DFA, b: DFA): DFA {
  return productDfa(a, b, "union");
}

export function dfaUnionAll(languages: DFA[]): DFA {
  if (languages.length === 0) {
    return {
      version: 1,
      alphabet: DFA_ALPHABET_ID,
      start: 0,
      accepting: [false],
      transitions: [new Array<number>(DFA_ALPHABET_SIZE).fill(-1)],
    };
  }
  let result = languages[0];
  for (let index = 1; index < languages.length; index += 1) {
    result = dfaUnion(result, languages[index]);
  }
  return result;
}

export function dfaIntersectionAll(languages: DFA[]): DFA {
  if (languages.length === 0) return compileRegex(".*");
  let result = languages[0];
  for (let index = 1; index < languages.length; index += 1) {
    result = dfaIntersection(result, languages[index]);
  }
  return result;
}

export function dfaComplement(dfa: DFA): DFA {
  const complete = completeDfa(dfa);
  return {
    ...complete,
    accepting: complete.accepting.map((value) => !value),
  };
}

export function dfaDifference(a: DFA, b: DFA): DFA {
  return dfaIntersection(a, dfaComplement(b));
}

export function dfaSubset(a: DFA, b: DFA): boolean {
  return dfaIsEmpty(dfaDifference(a, b));
}

export function dfaEquivalent(a: DFA, b: DFA): boolean {
  return dfaDigest(a) === dfaDigest(b);
}

export function dfaDigest(dfa: DFA): string {
  return stableDigest(serializeDfa(minimizeDfa(dfa)));
}

export function canonicalDfa(dfa: DFA): DFA {
  return minimizeDfa(dfa);
}

function escapeRegexLiteral(text: string): string {
  return text.replace(/[\\^$.*+?()[\]{}|]/g, "\\$&");
}

export function domainSuffixLanguage(suffix: string): DFA {
  let normalized = suffix.trim().toLowerCase();
  const at = normalized.lastIndexOf("@");
  if (at >= 0) normalized = normalized.slice(at + 1);
  normalized = normalized.replace(/^\.+/, "").replace(/\.+$/, "");
  if (!normalized) throw new TypeError("domain suffix must not be empty");
  const labels = "(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)*";
  return compileRegexCached(`${labels}${escapeRegexLiteral(normalized)}`, { ignoreCase: true });
}

export function domainMatchesLanguage(language: DFA, domain: string): boolean {
  let normalized = domain.trim().toLowerCase();
  if (normalized.endsWith(".")) normalized = normalized.slice(0, -1);
  return dfaAccepts(language, normalized);
}
