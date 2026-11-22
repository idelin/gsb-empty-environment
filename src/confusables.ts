import { digestValue } from "./canonical.ts";
import type {
  ConfusableRule,
  ConfusableTable,
  Scalar,
  SkeletonSymbol,
} from "./types.ts";
import {
  assertUnicodeScalar,
  codePoints,
  isHighSurrogate,
  isLowSurrogate,
} from "./unicode.ts";

export interface ConfusableMatch {
  readonly length: number;
  readonly skeleton: readonly SkeletonSymbol[];
  readonly order: number;
}

export interface ConfusableStep {
  readonly emitted: readonly SkeletonSymbol[];
  readonly pending: readonly Scalar[];
}

export interface TrieNode {
  readonly depth: number;
  readonly children: Map<Scalar, TrieNode>;
  output?: {
    readonly order: number;
    readonly skeleton: readonly SkeletonSymbol[];
  };
}

export interface ConfusableProgram {
  readonly table: ConfusableTable;
  readonly root: TrieNode;
  readonly maxDepth: number;
  readonly ruleCount: number;
  matchAt(scalars: readonly Scalar[], index: number): ConfusableMatch | null;
  step(pending: readonly Scalar[], scalar: Scalar): ConfusableStep;
  flush(pending: readonly Scalar[]): SkeletonSymbol[];
}

export interface ConfusableMatcher {
  readonly program: ConfusableProgram;
  push(input: Iterable<Scalar> | string): SkeletonSymbol[];
  end(): SkeletonSymbol[];
  reset(): void;
  pending(): readonly Scalar[];
}

function makeNode(depth: number): TrieNode {
  return { depth, children: new Map() };
}

export function assertConfusableTable(table: ConfusableTable): void {
  if (!table || typeof table.version !== "string" || table.version.length === 0) {
    throw new TypeError("confusable table needs a non-empty version");
  }
  if (!Array.isArray(table.rules)) {
    throw new TypeError("confusable table rules must be an array");
  }
  table.rules.forEach((rule, index) => {
    const context = `confusable rule ${index}`;
    if (!Array.isArray(rule.consumes) || rule.consumes.length < 1) {
      throw new TypeError(`${context}: must consume at least one scalar`);
    }
    if (rule.consumes.length > 3) {
      throw new TypeError(`${context}: may consume at most three scalars`);
    }
    for (const scalar of rule.consumes) {
      assertUnicodeScalar(scalar, context);
    }
    if (!Array.isArray(rule.skeleton) || rule.skeleton.length > 3) {
      throw new TypeError(`${context}: may emit at most three skeleton symbols`);
    }
    for (const symbol of rule.skeleton) {
      assertUnicodeScalar(symbol, `${context} skeleton`);
    }
  });
}

function insertRule(
  root: TrieNode,
  rule: ConfusableRule,
  order: number,
): void {
  let node = root;
  for (const scalar of rule.consumes) {
    let child = node.children.get(scalar);
    if (!child) {
      child = makeNode(node.depth + 1);
      node.children.set(scalar, child);
    }
    node = child;
  }
  // Equal consume sequences tie by declaration order: keep the first rule.
  if (!node.output) {
    node.output = { order, skeleton: rule.skeleton.slice() };
  }
}

function canDefer(program: ConfusableProgram, work: readonly Scalar[]): boolean {
  if (work.length >= program.maxDepth) return false;
  let node = program.root;
  for (const scalar of work) {
    const next = node.children.get(scalar);
    if (!next) return false;
    node = next;
  }
  return node.children.size > 0;
}

function bestMatch(
  program: ConfusableProgram,
  work: readonly Scalar[],
): ConfusableMatch | null {
  let node = program.root;
  let best: ConfusableMatch | null = null;
  const limit = Math.min(program.maxDepth, work.length);
  for (let i = 0; i < limit; i++) {
    const next = node.children.get(work[i]);
    if (!next) break;
    node = next;
    if (next.output) {
      best = {
        length: i + 1,
        skeleton: next.output.skeleton,
        order: next.output.order,
      };
    }
  }
  return best;
}

function run(
  program: ConfusableProgram,
  work: Scalar[],
  defer: boolean,
  output: SkeletonSymbol[],
): Scalar[] {
  let rest = work;
  while (rest.length > 0) {
    if (defer && canDefer(program, rest)) break;
    const match = bestMatch(program, rest);
    if (match) {
      output.push(...match.skeleton);
      rest = rest.slice(match.length);
    } else {
      output.push(rest[0]);
      rest = rest.slice(1);
    }
  }
  return rest;
}

export function createConfusableProgram(
  table: ConfusableTable,
): ConfusableProgram {
  assertConfusableTable(table);
  const root = makeNode(0);
  table.rules.forEach((rule, order) => insertRule(root, rule, order));
  let maxDepth = 1;
  for (const rule of table.rules) maxDepth = Math.max(maxDepth, rule.consumes.length);

  const program: ConfusableProgram = {
    table,
    root,
    maxDepth,
    ruleCount: table.rules.length,
    matchAt(scalars, index) {
      if (index < 0 || index >= scalars.length) return null;
      return bestMatch(program, scalars.slice(index, index + maxDepth));
    },
    step(pending, scalar) {
      assertUnicodeScalar(scalar, "confusable input");
      const work = [...pending, scalar];
      const emitted: SkeletonSymbol[] = [];
      const rest = run(program, work, true, emitted);
      return { emitted, pending: rest };
    },
    flush(pending) {
      const emitted: SkeletonSymbol[] = [];
      const rest = run(program, pending.slice(), false, emitted);
      if (rest.length !== 0) {
        throw new Error("internal error: flush left pending scalars");
      }
      return emitted;
    },
  };
  return program;
}

function asProgram(
  tableOrProgram: ConfusableTable | ConfusableProgram,
): ConfusableProgram {
  return "root" in tableOrProgram
    ? tableOrProgram
    : createConfusableProgram(tableOrProgram);
}

export function createConfusableMatcher(
  tableOrProgram: ConfusableTable | ConfusableProgram,
): ConfusableMatcher {
  const program = asProgram(tableOrProgram);
  let pending: Scalar[] = [];
  let textCarry = "";

  function scalarsFromChunk(
    input: Iterable<Scalar> | string,
  ): Scalar[] {
    if (typeof input === "string") {
      let text = textCarry + input;
      textCarry = "";
      if (
        text.length > 0 &&
        isHighSurrogate(text.charCodeAt(text.length - 1))
      ) {
        textCarry = text.slice(-1);
        text = text.slice(0, -1);
      }
      return codePoints(text);
    }
    if (textCarry.length > 0) {
      throw new TypeError(
        "cannot mix a pending UTF-16 half with scalar-array input",
      );
    }
    return Array.from(input);
  }

  return {
    program,
    push(input) {
      const emitted: SkeletonSymbol[] = [];
      for (const scalar of scalarsFromChunk(input)) {
        const step = program.step(pending, scalar);
        pending = step.pending.slice();
        emitted.push(...step.emitted);
      }
      return emitted;
    },
    end() {
      if (textCarry.length > 0) {
        throw new TypeError("input ended in the middle of a surrogate pair");
      }
      const emitted = program.flush(pending);
      pending = [];
      return emitted;
    },
    reset() {
      pending = [];
      textCarry = "";
    },
    pending() {
      return pending.slice();
    },
  };
}

export function transcribe(
  tableOrProgram: ConfusableTable | ConfusableProgram,
  input: Iterable<Scalar> | string,
): SkeletonSymbol[] {
  const matcher = createConfusableMatcher(tableOrProgram);
  const output = matcher.push(input);
  output.push(...matcher.end());
  return output;
}

/** Skeleton of one label. */
export function skeletonLabel(
  tableOrProgram: ConfusableTable | ConfusableProgram,
  label: Iterable<Scalar> | string,
): SkeletonSymbol[] {
  return transcribe(tableOrProgram, label);
}

/**
 * Skeleton of a full domain.  Labels are joined with an ASCII full stop and
 * the resulting scalar stream is transcribed as a whole, so a table may
 * deliberately contain rules that cross a label separator.
 */
export function skeletonDomain(
  tableOrProgram: ConfusableTable | ConfusableProgram,
  labels: readonly (Iterable<Scalar> | string)[],
): SkeletonSymbol[] {
  const program = asProgram(tableOrProgram);
  const scalars: Scalar[] = [];
  labels.forEach((label, index) => {
    if (index > 0) scalars.push(0x2e);
    if (typeof label === "string") {
      scalars.push(...codePoints(label));
    } else {
      for (const scalar of label) scalars.push(scalar);
    }
  });
  return transcribe(program, scalars);
}

export function confusableTableDigest(table: ConfusableTable): string {
  return digestValue({
    kind: "confusable-table",
    version: table.version,
    rules: table.rules.map((rule) => ({
      consumes: rule.consumes,
      skeleton: rule.skeleton,
    })),
  });
}

/** Useful for the checker: decode a skeleton back to a display string. */
export function skeletonText(skeleton: readonly Scalar[]): string {
  return String.fromCodePoint(...skeleton);
}
