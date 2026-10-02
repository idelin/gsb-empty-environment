import type {
  Activation,
  AlphaTest,
  Condition,
  Fact,
  JoinTest,
  Op,
  Rule,
  Value,
  WME,
} from "./types.ts";
import { NegativeToken, Token, type TokenOwner } from "./token.ts";

const OPS: Record<Op, (a: Value, b: Value) => boolean> = {
  eq: (a, b) => a === b,
  ne: (a, b) => a !== b,
  lt: (a, b) => (a as number) < (b as number),
  lte: (a, b) => (a as number) <= (b as number),
  gt: (a, b) => (a as number) > (b as number),
  gte: (a, b) => (a as number) >= (b as number),
};

function passesTests(tests: AlphaTest[], fact: Fact): boolean {
  for (const test of tests) {
    if (!OPS[test.op](fact.attrs[test.field] ?? null, test.value)) return false;
  }
  return true;
}

/** Alpha memory: every WME of one fact type that passes the constant tests. */
class AlphaMemory {
  readonly items = new Map<number, WME>();
  readonly successors: JoinNode[] = [];
  readonly factType: string;
  readonly tests: AlphaTest[];

  constructor(factType: string, tests: AlphaTest[]) {
    this.factType = factType;
    this.tests = tests;
  }

  activate(wme: WME): void {
    this.items.set(wme.id, wme);
    for (const successor of this.successors) successor.rightActivate(wme);
  }

  retract(wme: WME): void {
    this.items.delete(wme.id);
    for (const successor of this.successors) successor.rightRetract(wme);
  }
}

/** Beta memory: holds tokens and propagates new ones to successor nodes. */
class BetaMemory implements TokenOwner {
  readonly tokens: Token[] = [];
  readonly children: Array<JoinNode | ProductionNode> = [];

  leftActivate(token: Token): void {
    this.tokens.push(token);
    for (const child of this.children) child.leftActivate(token);
  }
}

/** Join node: combines tokens from its left parent with WMEs from an alpha memory. */
class JoinNode implements TokenOwner {
  readonly tokens: Token[] = [];
  readonly children: Array<JoinNode | ProductionNode> = [];
  readonly parent: BetaMemory | JoinNode;
  readonly alpha: AlphaMemory;
  readonly joinTests: JoinTest[];
  readonly binds: Record<string, string>;

  constructor(
    parent: BetaMemory | JoinNode,
    alpha: AlphaMemory,
    joinTests: JoinTest[],
    binds: Record<string, string>,
  ) {
    this.parent = parent;
    this.alpha = alpha;
    this.joinTests = joinTests;
    this.binds = binds;
    parent.children.push(this);
    alpha.successors.push(this);
  }

  private parentTokens(): Token[] {
    return this.parent.tokens;
  }

  private matches(token: Token, wme: WME): boolean {
    for (const test of this.joinTests) {
      if (token.bindings.get(test.varName) !== wme.fact.attrs[test.field]) {
        return false;
      }
    }
    return true;
  }

  private emit(parent: Token, wme: WME): void {
    const bindings = new Map(parent.bindings);
    for (const [field, varName] of Object.entries(this.binds)) {
      bindings.set(varName, wme.fact.attrs[field] ?? null);
    }
    const token = new Token(this, parent, wme, bindings);
    this.tokens.push(token);
    for (const child of this.children) child.leftActivate(token);
  }

  /** New token arrived from the left: try it against every WME in the alpha memory. */
  leftActivate(token: Token): void {
    for (const wme of this.alpha.items.values()) {
      if (this.matches(token, wme)) this.emit(token, wme);
    }
  }

  /** New WME arrived from the right: try it against every token on the left. */
  rightActivate(wme: WME): void {
    for (const token of this.parentTokens()) {
      if (this.matches(token, wme)) this.emit(token, wme);
    }
  }

  /**
   * A WME was retracted: every token in this node's output that was built
   * from it is destroyed via a negative token, cascading down the network.
   */
  rightRetract(wme: WME): void {
    for (const token of [...this.tokens]) {
      if (token.wme === wme) new NegativeToken(token).annihilate();
    }
  }
}

/** Production node: terminal of a rule; maintains the conflict set entries. */
class ProductionNode implements TokenOwner {
  readonly tokens: Token[] = [];
  readonly activations = new Map<number, Activation>();
  readonly ruleName: string;
  readonly agenda: Map<number, Activation>;

  constructor(
    parent: BetaMemory | JoinNode,
    ruleName: string,
    agenda: Map<number, Activation>,
  ) {
    this.ruleName = ruleName;
    this.agenda = agenda;
    parent.children.push(this);
  }

  leftActivate(token: Token): void {
    const terminal = new Token(this, token, null, token.bindings);
    this.tokens.push(terminal);
    const activation: Activation = {
      rule: this.ruleName,
      bindings: new Map(terminal.bindings),
      wmes: terminal.wmeChain(),
    };
    this.activations.set(terminal.id, activation);
    this.agenda.set(terminal.id, activation);
  }

  removeActivation(token: Token): void {
    this.activations.delete(token.id);
    this.agenda.delete(token.id);
  }
}

function compileWhere(condition: Condition): AlphaTest[] {
  const tests: AlphaTest[] = [];
  for (const [field, constraint] of Object.entries(condition.where ?? {})) {
    if (constraint !== null && typeof constraint === "object") {
      for (const [op, value] of Object.entries(constraint)) {
        tests.push({ field, op: op as Op, value: value as Value });
      }
    } else {
      tests.push({ field, op: "eq", value: constraint as Value });
    }
  }
  return tests;
}

function alphaKey(factType: string, tests: AlphaTest[]): string {
  const parts = tests
    .map((t) => `${t.field}${t.op}${JSON.stringify(t.value)}`)
    .sort();
  return `${factType}|${parts.join("&")}`;
}

/**
 * Incremental RETE network.
 *
 * - Alpha network: shared constant-test memories filter incoming facts.
 * - Beta network: join nodes incrementally combine partial matches.
 * - Retraction: negative tokens annihilate exactly the affected tokens and
 *   cascade to descendants — the fact base is never rescanned.
 */
export class ReteNetwork {
  private readonly wmes = new Map<number, WME>();
  private readonly alphaMemories = new Map<string, AlphaMemory>();
  private readonly topMemory = new BetaMemory();
  private readonly agenda = new Map<number, Activation>();
  private nextWmeId = 1;

  constructor() {
    // Dummy top token: the seed every first-level join matches against.
    this.topMemory.tokens.push(new Token(this.topMemory, null, null, new Map()));
  }

  /** Compile a rule into alpha memories, a join chain and a production node. */
  addRule(rule: Rule): void {
    if (rule.conditions.length === 0) {
      throw new Error(`rule "${rule.name}" needs at least one condition`);
    }
    const boundVars = new Set<string>();
    let parent: BetaMemory | JoinNode = this.topMemory;

    for (const condition of rule.conditions) {
      const tests = compileWhere(condition);
      const key = alphaKey(condition.type, tests);
      let alpha = this.alphaMemories.get(key);
      if (!alpha) {
        alpha = new AlphaMemory(condition.type, tests);
        this.alphaMemories.set(key, alpha);
        // Catch the new memory up with already-asserted facts.
        for (const wme of this.wmes.values()) {
          if (wme.fact.type === condition.type && passesTests(tests, wme.fact)) {
            alpha.items.set(wme.id, wme);
          }
        }
      }

      const binds = condition.bind ?? {};
      const joinTests: JoinTest[] = [];
      for (const [field, varName] of Object.entries(binds)) {
        if (boundVars.has(varName)) joinTests.push({ varName, field });
      }
      for (const varName of Object.values(binds)) boundVars.add(varName);

      const join = new JoinNode(parent, alpha, joinTests, binds);
      // Left-activate the new join with tokens that already exist above it.
      for (const token of [...parent.tokens]) join.leftActivate(token);
      parent = join;
    }

    const production = new ProductionNode(parent, rule.name, this.agenda);
    for (const token of [...parent.tokens]) production.leftActivate(token);
  }

  /** Assert a fact; returns its WME id. Activation propagates incrementally. */
  addFact(fact: Fact): number {
    const wme: WME = { id: this.nextWmeId++, fact };
    this.wmes.set(wme.id, wme);
    for (const alpha of this.alphaMemories.values()) {
      if (alpha.factType === fact.type && passesTests(alpha.tests, fact)) {
        alpha.activate(wme);
      }
    }
    return wme.id;
  }

  /**
   * Retract a fact by WME id. Negative tokens remove exactly the tokens and
   * conflict-set entries derived from it; everything else stays untouched.
   */
  retractFact(wmeId: number): boolean {
    const wme = this.wmes.get(wmeId);
    if (!wme) return false;
    this.wmes.delete(wmeId);
    for (const alpha of this.alphaMemories.values()) {
      if (alpha.items.has(wme.id)) alpha.retract(wme);
    }
    return true;
  }

  /** Current conflict set: one activation per fully matched rule instance. */
  getConflictSet(): Activation[] {
    return [...this.agenda.values()];
  }

  /** Introspection helper for tests and debugging. */
  stats(): { wmes: number; alphaMemories: number; agendaSize: number } {
    return {
      wmes: this.wmes.size,
      alphaMemories: this.alphaMemories.size,
      agendaSize: this.agenda.size,
    };
  }
}
