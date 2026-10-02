export type ConstOp = 'eq' | 'neq' | 'gt' | 'gte' | 'lt' | 'lte';

export type FieldConstraint =
  | { kind: 'const'; value: unknown }
  | { kind: 'cmp'; op: ConstOp; value: unknown }
  | { kind: 'var'; name: string };

export interface Condition {
  type: string;
  fields?: Record<string, FieldConstraint>;
}

export interface RuleDef {
  name: string;
  conditions: Condition[];
}

export interface Activation {
  rule: string;
  bindings: Record<string, unknown>;
  factIds: number[];
}

interface WME {
  id: number;
  type: string;
  attrs: Record<string, unknown>;
  tokens: Token[];
  memories: AlphaMemory[];
}

class Token {
  readonly parent: Token | null;
  readonly wme: WME | null;
  readonly bindings: Map<string, unknown>;
  readonly children: Token[] = [];
  owner: BetaMemory | TerminalNode | null = null;

  constructor(
    parent: Token | null,
    wme: WME | null,
    bindings: Map<string, unknown>,
  ) {
    this.parent = parent;
    this.wme = wme;
    this.bindings = bindings;
  }

  factIds(): number[] {
    const ids: number[] = [];
    let cursor: Token | null = this;
    while (cursor) {
      if (cursor.wme) ids.push(cursor.wme.id);
      cursor = cursor.parent;
    }
    return ids.reverse();
  }
}

class AlphaMemory {
  readonly wmes: WME[] = [];
  readonly successors: JoinNode[] = [];
}

type ConstTest = (attrs: Record<string, unknown>) => boolean;

interface ConstTestDescriptor {
  attr: string;
  op: ConstOp;
  value: unknown;
}

class AlphaNode {
  readonly factType: string;
  readonly test: ConstTest;
  readonly memory = new AlphaMemory();

  constructor(factType: string, test: ConstTest) {
    this.factType = factType;
    this.test = test;
  }
}

interface JoinTest {
  varName: string;
  wmeAttr: string;
}

interface VarBinding {
  varName: string;
  wmeAttr: string;
}

class JoinNode {
  readonly leftParent: BetaMemory;
  readonly rightParent: AlphaMemory;
  readonly tests: JoinTest[];
  readonly newVars: VarBinding[];
  readonly children: Array<BetaMemory | TerminalNode> = [];

  constructor(
    leftParent: BetaMemory,
    rightParent: AlphaMemory,
    tests: JoinTest[],
    newVars: VarBinding[],
  ) {
    this.leftParent = leftParent;
    this.rightParent = rightParent;
    this.tests = tests;
    this.newVars = newVars;
  }

  private matches(token: Token, wme: WME): boolean {
    for (const test of this.tests) {
      if (token.bindings.get(test.varName) !== wme.attrs[test.wmeAttr]) return false;
    }
    return true;
  }

  private makeToken(parent: Token, wme: WME): Token {
    const bindings = new Map(parent.bindings);
    for (const binding of this.newVars) {
      bindings.set(binding.varName, wme.attrs[binding.wmeAttr]);
    }
    const token = new Token(parent, wme, bindings);
    parent.children.push(token);
    wme.tokens.push(token);
    return token;
  }

  private propagate(token: Token): void {
    for (const child of this.children) child.leftActivate(token);
  }

  rightActivate(wme: WME): void {
    for (const token of this.leftParent.tokens) {
      if (this.matches(token, wme)) this.propagate(this.makeToken(token, wme));
    }
  }

  leftActivate(token: Token): void {
    for (const wme of this.rightParent.wmes) {
      if (this.matches(token, wme)) this.propagate(this.makeToken(token, wme));
    }
  }

  rightUnactivate(wme: WME): void {
    for (const token of [...wme.tokens]) deleteToken(token);
  }
}

class BetaMemory {
  readonly tokens: Token[] = [];
  readonly children: JoinNode[] = [];

  leftActivate(token: Token): void {
    token.owner = this;
    this.tokens.push(token);
    for (const join of this.children) join.leftActivate(token);
  }

  leftUnactivate(token: Token): void {
    const index = this.tokens.indexOf(token);
    if (index >= 0) this.tokens.splice(index, 1);
  }
}

class TerminalNode {
  readonly ruleName: string;
  readonly engine: ReteEngine;
  readonly tokens: Token[] = [];

  constructor(ruleName: string, engine: ReteEngine) {
    this.ruleName = ruleName;
    this.engine = engine;
  }

  leftActivate(token: Token): void {
    token.owner = this;
    this.tokens.push(token);
    this.engine.addActivation(this, token);
  }

  leftUnactivate(token: Token): void {
    const index = this.tokens.indexOf(token);
    if (index >= 0) {
      this.tokens.splice(index, 1);
      this.engine.removeActivation(token);
    }
  }
}

function deleteToken(token: Token): void {
  for (const child of [...token.children]) deleteToken(child);
  const owner = token.owner;
  if (owner instanceof BetaMemory) owner.leftUnactivate(token);
  else if (owner instanceof TerminalNode) owner.leftUnactivate(token);
  if (token.wme) {
    const index = token.wme.tokens.indexOf(token);
    if (index >= 0) token.wme.tokens.splice(index, 1);
  }
  if (token.parent) {
    const index = token.parent.children.indexOf(token);
    if (index >= 0) token.parent.children.splice(index, 1);
  }
}

function compare(op: ConstOp, left: unknown, right: unknown): boolean {
  switch (op) {
    case 'eq':
      return left === right;
    case 'neq':
      return left !== right;
    case 'gt':
      return (left as number) > (right as number);
    case 'gte':
      return (left as number) >= (right as number);
    case 'lt':
      return (left as number) < (right as number);
    case 'lte':
      return (left as number) <= (right as number);
  }
}

function buildConstTest(descriptors: ConstTestDescriptor[]): ConstTest {
  return (attrs) =>
    descriptors.every((d) => compare(d.op, attrs[d.attr], d.value));
}

export class ReteEngine {
  private readonly wmes = new Map<number, WME>();
  private readonly alphaNodes: AlphaNode[] = [];
  private readonly alphaByKey = new Map<string, AlphaNode>();
  private readonly rootMemory = new BetaMemory();
  private readonly conflictSet = new Map<Token, Activation>();
  private nextFactId = 1;

  constructor() {
    const rootToken = new Token(null, null, new Map());
    rootToken.owner = this.rootMemory;
    this.rootMemory.tokens.push(rootToken);
  }

  addFact(type: string, attrs: Record<string, unknown> = {}): number {
    const wme: WME = {
      id: this.nextFactId++,
      type,
      attrs: { ...attrs },
      tokens: [],
      memories: [],
    };
    this.wmes.set(wme.id, wme);
    for (const node of this.alphaNodes) {
      if (node.factType !== type || !node.test(wme.attrs)) continue;
      node.memory.wmes.push(wme);
      wme.memories.push(node.memory);
      for (const join of node.memory.successors) join.rightActivate(wme);
    }
    return wme.id;
  }

  retractFact(id: number): boolean {
    const wme = this.wmes.get(id);
    if (!wme) return false;
    this.wmes.delete(id);
    for (const memory of wme.memories) {
      const index = memory.wmes.indexOf(wme);
      if (index >= 0) memory.wmes.splice(index, 1);
      for (const join of memory.successors) join.rightUnactivate(wme);
    }
    return true;
  }

  addRule(def: RuleDef): void {
    if (def.conditions.length === 0) {
      throw new Error(`rule "${def.name}" must have at least one condition`);
    }
    const bound = new Set<string>();
    let left = this.rootMemory;
    def.conditions.forEach((cond, index) => {
      const constTests: ConstTestDescriptor[] = [];
      const joinTests: JoinTest[] = [];
      const newVars: VarBinding[] = [];
      for (const [attr, constraint] of Object.entries(cond.fields ?? {})) {
        if (constraint.kind === 'const') {
          constTests.push({ attr, op: 'eq', value: constraint.value });
        } else if (constraint.kind === 'cmp') {
          constTests.push({ attr, op: constraint.op, value: constraint.value });
        } else if (bound.has(constraint.name)) {
          joinTests.push({ varName: constraint.name, wmeAttr: attr });
        } else {
          newVars.push({ varName: constraint.name, wmeAttr: attr });
          bound.add(constraint.name);
        }
      }
      const alpha = this.getOrCreateAlpha(cond.type, constTests);
      const leftParent = left;
      const join = new JoinNode(leftParent, alpha.memory, joinTests, newVars);
      alpha.memory.successors.push(join);
      leftParent.children.push(join);
      const isLast = index === def.conditions.length - 1;
      if (isLast) {
        join.children.push(new TerminalNode(def.name, this));
      } else {
        const out = new BetaMemory();
        join.children.push(out);
        left = out;
      }
      for (const token of leftParent.tokens) join.leftActivate(token);
    });
  }

  getActivations(): Activation[] {
    return [...this.conflictSet.values()];
  }

  hasFact(id: number): boolean {
    return this.wmes.has(id);
  }

  get factCount(): number {
    return this.wmes.size;
  }

  addActivation(terminal: TerminalNode, token: Token): void {
    this.conflictSet.set(token, {
      rule: terminal.ruleName,
      bindings: Object.fromEntries(token.bindings),
      factIds: token.factIds(),
    });
  }

  removeActivation(token: Token): void {
    this.conflictSet.delete(token);
  }

  private getOrCreateAlpha(
    type: string,
    descriptors: ConstTestDescriptor[],
  ): AlphaNode {
    const key = type + '|' + JSON.stringify(descriptors);
    let node = this.alphaByKey.get(key);
    if (!node) {
      node = new AlphaNode(type, buildConstTest(descriptors));
      this.alphaByKey.set(key, node);
      this.alphaNodes.push(node);
      for (const wme of this.wmes.values()) {
        if (wme.type === type && node.test(wme.attrs)) {
          node.memory.wmes.push(wme);
          wme.memories.push(node.memory);
        }
      }
    }
    return node;
  }
}
