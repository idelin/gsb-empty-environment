import {
  arrayDescriptor,
  canonicalJson,
  ctorId,
  ctorKey,
  sha256Hex,
} from "./canonical.ts";
import type {
  BindingTable,
  CanonicalGrammar,
  CanonicalProduction,
  ConstructorDescriptor,
  GrammarDocument,
} from "./types.ts";

export class GrammarError extends Error {}

interface ResolvedProduction {
  ctor: ConstructorDescriptor;
  children: string[];
}

/**
 * Build a canonical, rename-independent grammar via greatest-fixpoint
 * partition refinement (strong bisimulation on the constructor tree view).
 * Bisimilar nonterminals collapse to one id, so certificates stay stable when
 * host-side nonterminal names change without changing the accepted language.
 */
export function canonicalizeGrammar(
  document: GrammarDocument,
  table: BindingTable,
): CanonicalGrammar {
  const catalog = buildCatalog(table);
  const resolved = resolveDocument(document, catalog);
  const names = [...resolved.keys()].sort();
  if (!resolved.has(document.start)) {
    throw new GrammarError(`start nonterminal ${document.start} is undefined`);
  }
  if (resolved.get(document.start)!.length === 0) {
    throw new GrammarError(
      `start nonterminal ${document.start} has no resolvable productions`,
    );
  }

  let labels = initialLabels(names, resolved);
  for (;;) {
    const next = refineOnce(labels, resolved, names);
    if (samePartition(labels, next, names)) {
      labels = next;
      break;
    }
    labels = next;
  }

  const nonterminals = new Map<string, CanonicalProduction[]>();
  for (const name of names) {
    const id = labels.get(name)!;
    const productions = resolved.get(name)!.map<CanonicalProduction>((production) => ({
      ctorId: ctorId(production.ctor),
      children: production.children.map((child) => labels.get(child)!),
    }));
    const existing = nonterminals.get(id);
    if (existing) {
      existing.push(...productions);
    } else {
      nonterminals.set(id, productions);
    }
  }

  for (const productions of nonterminals.values()) {
    productions.sort((a, b) => {
      if (a.ctorId !== b.ctorId) {
        return a.ctorId < b.ctorId ? -1 : 1;
      }
      return compareArrays(a.children, b.children);
    });
  }

  const start = labels.get(document.start)!;
  const digest = sha256Hex({
    rules: table.rulesVersion,
    catalog: [...catalog.values()]
      .map((descriptor) => ({ id: ctorId(descriptor), descriptor }))
      .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)),
    start,
    nonterminals: [...nonterminals.entries()]
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
      .map(([id, productions]) => ({ id, productions })),
  });

  return { tenant: document.tenant, start, nonterminals, catalog, digest };
}

function buildCatalog(
  table: BindingTable,
): Map<string, ConstructorDescriptor> {
  const catalog = new Map<string, ConstructorDescriptor>();
  for (const descriptor of table.constructors) {
    validateDescriptor(descriptor);
    const id = ctorId(descriptor);
    const prior = catalog.get(id);
    if (prior && canonicalJson(prior) !== canonicalJson(descriptor)) {
      throw new GrammarError(`conflicting constructor entries for ${id}`);
    }
    catalog.set(id, descriptor);
  }
  return catalog;
}

function validateDescriptor(descriptor: ConstructorDescriptor): void {
  if (descriptor.arity < 0) {
    throw new GrammarError("constructor arity must be non-negative");
  }
  if (descriptor.variances.length !== descriptor.arity) {
    throw new GrammarError(
      `variance count must equal arity for ${descriptor.typeName}`,
    );
  }
}

function resolveDocument(
  document: GrammarDocument,
  catalog: Map<string, ConstructorDescriptor>,
): Map<string, ResolvedProduction[]> {
  const resolved = new Map<string, ResolvedProduction[]>();
  for (const [name, productions] of Object.entries(document.nonterminals)) {
    const list: ResolvedProduction[] = [];
    for (const production of productions) {
      const descriptor = pickDescriptor(document, production.constructor, catalog);
      if (production.children.length !== descriptor.arity) {
        throw new GrammarError(
          `production on ${name} has wrong arity for ${descriptor.typeName}`,
        );
      }
      for (const child of production.children) {
        if (!document.nonterminals[child]) {
          throw new GrammarError(`unknown child nonterminal ${child}`);
        }
      }
      list.push({ ctor: descriptor, children: [...production.children] });
    }
    resolved.set(name, list);
  }
  return resolved;
}

function pickDescriptor(
  document: GrammarDocument,
  wanted: ConstructorDescriptor,
  catalog: Map<string, ConstructorDescriptor>,
): ConstructorDescriptor {
  const key = ctorKey(wanted.assemblyPublicKey, wanted.typeName);
  if (wanted.array) {
    const descriptor = arrayDescriptor(wanted.arrayRank ?? 1);
    catalog.set(ctorId(descriptor), descriptor);
    return descriptor;
  }
  const descriptor = catalog.get(ctorId(wanted));
  if (descriptor) {
    return descriptor;
  }
  throw new GrammarError(
    `grammar for tenant ${document.tenant} references constructor not in table: ${key}@${wanted.assemblyVersion}`,
  );
}

function initialLabels(
  names: string[],
  resolved: Map<string, ResolvedProduction[]>,
): Map<string, string> {
  const labels = new Map<string, string>();
  for (const name of names) {
    const signature = resolved
      .get(name)!
      .map((production) => [ctorId(production.ctor)])
      .sort((a, b) => compareArrays(a as string[], b as string[]));
    labels.set(name, `nt_${sha256Hex(signature).slice(0, 12)}`);
  }
  return labels;
}

function refineOnce(
  labels: Map<string, string>,
  resolved: Map<string, ResolvedProduction[]>,
  names: string[],
): Map<string, string> {
  const signatures = new Map<string, string>();
  for (const name of names) {
    const signature = canonicalJson(
      resolved
        .get(name)!
        .map((production) => ({
          c: ctorId(production.ctor),
          kids: production.children.map((child) => labels.get(child)!),
        }))
        .sort((a, b) => {
          if (a.c !== b.c) {
            return a.c < b.c ? -1 : 1;
          }
          return compareArrays(a.kids, b.kids);
        }),
    );
    signatures.set(name, signature);
  }
  const unique = [...new Set(signatures.values())].sort();
  const labelById = new Map<string, string>();
  unique.forEach((signature, index) => {
    labelById.set(
      signature,
      `nt_${sha256Hex(signature).slice(0, 10)}_${index.toString(36)}`,
    );
  });
  const next = new Map<string, string>();
  for (const name of names) {
    next.set(name, labelById.get(signatures.get(name)!)!);
  }
  return next;
}

function samePartition(
  before: Map<string, string>,
  after: Map<string, string>,
  names: string[],
): boolean {
  for (let i = 0; i < names.length; i++) {
    for (let j = i + 1; j < names.length; j++) {
      const sameBefore = before.get(names[i]) === before.get(names[j]);
      const sameAfter = after.get(names[i]) === after.get(names[j]);
      if (sameBefore !== sameAfter) {
        return false;
      }
    }
  }
  return true;
}

function compareArrays(a: string[], b: string[]): number {
  const length = Math.min(a.length, b.length);
  for (let i = 0; i < length; i++) {
    if (a[i] !== b[i]) {
      return a[i] < b[i] ? -1 : 1;
    }
  }
  return a.length - b.length;
}

export { arrayDescriptor };
