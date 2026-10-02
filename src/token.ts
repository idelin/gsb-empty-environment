import type { Value, WME } from "./types.ts";

let nextTokenId = 1;

/**
 * A positive token: a (partial) match flowing down the beta network.
 * Tokens form a parent/children tree so retraction can cascade precisely.
 */
export class Token {
  readonly id = nextTokenId++;
  readonly children = new Set<Token>();
  readonly node: TokenOwner;
  readonly parent: Token | null;
  readonly wme: WME | null;
  readonly bindings: Map<string, Value>;

  constructor(
    node: TokenOwner,
    parent: Token | null,
    wme: WME | null,
    bindings: Map<string, Value>,
  ) {
    this.node = node;
    this.parent = parent;
    this.wme = wme;
    this.bindings = bindings;
    parent?.children.add(this);
  }

  /** WME chain from the root down to this token (oldest first). */
  wmeChain(): WME[] {
    const chain: WME[] = [];
    let cursor: Token | null = this;
    while (cursor) {
      if (cursor.wme) chain.unshift(cursor.wme);
      cursor = cursor.parent;
    }
    return chain;
  }
}

/** Anything that owns a token list: beta memories, join nodes, production nodes. */
export interface TokenOwner {
  readonly tokens: Token[];
}

/**
 * A negative token: the retraction message that travels the exact same path
 * its positive counterpart took, annihilating it and cascading to descendants.
 * This is what makes retraction incremental — no fact-base rescan is involved.
 */
export class NegativeToken {
  readonly target: Token;

  constructor(target: Token) {
    this.target = target;
  }

  /** Annihilate the target token and cascade the deletion to its children. */
  annihilate(): void {
    const target = this.target;
    for (const child of [...target.children]) {
      new NegativeToken(child).annihilate();
    }
    target.parent?.children.delete(target);
    const list = target.node.tokens;
    const index = list.indexOf(target);
    if (index !== -1) list.splice(index, 1);
    const node = target.node;
    if (isProductionNode(node)) node.removeActivation(target);
  }
}

export interface ProductionNodeLike extends TokenOwner {
  removeActivation(token: Token): void;
}

function isProductionNode(node: TokenOwner): node is ProductionNodeLike {
  return typeof (node as ProductionNodeLike).removeActivation === "function";
}
