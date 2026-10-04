export type AttributeValue = string | number | boolean | null;

export interface AttributeSpec {
  name: string;
  cost: number;
}

export interface CandidateSpec {
  name: string;
  values: Record<string, AttributeValue>;
}

export interface Problem {
  attributes: AttributeSpec[];
  candidates: CandidateSpec[];
}

export interface LeafNode {
  kind: 'leaf';
  candidates: string[];
}

export interface Branch {
  value: AttributeValue;
  candidates: string[];
  child: TreeNode;
}

export interface InternalNode {
  kind: 'node';
  attribute: string;
  attributeCost: number;
  branches: Branch[];
}

export type TreeNode = LeafNode | InternalNode;

export interface SolveResult {
  worstCaseCost: number;
  tree: TreeNode;
  equivalenceGroups: string[][];
}

export const MAX_CANDIDATES = 18;

interface DpEntry {
  cost: number;
  attributeIndex: number; // -1 means this subset is a leaf (single equivalence class)
}

export function solveDecisionTree(problem: Problem): SolveResult {
  validateProblem(problem);

  const attributes = problem.attributes;
  const candidates = problem.candidates;
  const n = candidates.length;
  const m = attributes.length;

  // Stable key per (candidate, attribute) so values of different types never collide.
  const valueKeys: string[][] = candidates.map((cand) =>
    attributes.map((attr) => JSON.stringify(cand.values[attr.name] ?? null)),
  );
  const valueOf: AttributeValue[][] = candidates.map((cand) =>
    attributes.map((attr) => cand.values[attr.name] ?? null),
  );

  // Candidates with identical signatures can never be distinguished.
  const signatureIds = new Map<string, number>();
  const sigOf: number[] = valueKeys.map((keys) => {
    const sig = keys.join('\u0001');
    let id = signatureIds.get(sig);
    if (id === undefined) {
      id = signatureIds.size;
      signatureIds.set(sig, id);
    }
    return id;
  });

  // DP over candidate subsets (bitmask). solve(mask) = minimal worst-case
  // total cost to distinguish every distinguishable candidate inside mask.
  // Recurrence (fully adaptive: the next attribute is chosen per subset):
  //   solve(S) = 0                                        if all of S shares one signature
  //   solve(S) = min_a [ cost(a) + max_G solve(G) ]       over attributes a that split S,
  //              where G ranges over the value-groups of a inside S
  const memo = new Map<number, DpEntry>();

  const solve = (mask: number): DpEntry => {
    const cached = memo.get(mask);
    if (cached !== undefined) return cached;

    let first = -1;
    let allSame = true;
    for (let rest = mask; rest !== 0; rest &= rest - 1) {
      const j = 31 - Math.clz32(rest & -rest);
      if (first === -1) {
        first = j;
      } else if (sigOf[j] !== sigOf[first]) {
        allSame = false;
        break;
      }
    }

    let entry: DpEntry;
    if (allSame) {
      entry = { cost: 0, attributeIndex: -1 };
    } else {
      let bestCost = Infinity;
      let bestAttr = -1;
      for (let a = 0; a < m; a++) {
        const parts = new Map<string, number>();
        for (let rest = mask; rest !== 0; rest &= rest - 1) {
          const j = 31 - Math.clz32(rest & -rest);
          const key = valueKeys[j][a];
          parts.set(key, (parts.get(key) ?? 0) | (1 << j));
        }
        if (parts.size < 2) continue; // attribute does not split this subset
        let worst = 0;
        for (const sub of parts.values()) {
          const subCost = solve(sub).cost;
          if (subCost > worst) worst = subCost;
        }
        const total = attributes[a].cost + worst;
        if (total < bestCost) {
          bestCost = total;
          bestAttr = a;
        }
      }
      entry = { cost: bestCost, attributeIndex: bestAttr };
    }
    memo.set(mask, entry);
    return entry;
  };

  const namesOf = (mask: number): string[] => {
    const names: string[] = [];
    for (let rest = mask; rest !== 0; rest &= rest - 1) {
      names.push(candidates[31 - Math.clz32(rest & -rest)].name);
    }
    return names;
  };

  const buildTree = (mask: number): TreeNode => {
    const entry = solve(mask);
    if (entry.attributeIndex === -1) {
      return { kind: 'leaf', candidates: namesOf(mask) };
    }
    const a = entry.attributeIndex;
    const parts = new Map<string, number>();
    for (let rest = mask; rest !== 0; rest &= rest - 1) {
      const j = 31 - Math.clz32(rest & -rest);
      const key = valueKeys[j][a];
      parts.set(key, (parts.get(key) ?? 0) | (1 << j));
    }
    const branches: Branch[] = [...parts.entries()]
      .sort(([k1], [k2]) => (k1 < k2 ? -1 : k1 > k2 ? 1 : 0))
      .map(([, sub]) => {
        const rep = 31 - Math.clz32(sub & -sub);
        return {
          value: valueOf[rep][a],
          candidates: namesOf(sub),
          child: buildTree(sub),
        };
      });
    return {
      kind: 'node',
      attribute: attributes[a].name,
      attributeCost: attributes[a].cost,
      branches,
    };
  };

  const fullMask = (1 << n) - 1;
  const tree = buildTree(fullMask);
  const worstCaseCost = solve(fullMask).cost;

  const groupsBySig = new Map<number, string[]>();
  for (let j = 0; j < n; j++) {
    const id = sigOf[j];
    const group = groupsBySig.get(id);
    if (group) group.push(candidates[j].name);
    else groupsBySig.set(id, [candidates[j].name]);
  }
  const equivalenceGroups = [...groupsBySig.values()].filter((g) => g.length > 1);

  return { worstCaseCost, tree, equivalenceGroups };
}

export function formatDecisionTree(result: SolveResult): string {
  const lines: string[] = [];
  lines.push(`Worst-case total cost: ${result.worstCaseCost}`);
  if (result.equivalenceGroups.length > 0) {
    const groups = result.equivalenceGroups.map((g) => `[${g.join(', ')}]`).join(' ');
    lines.push(`Equivalence groups (indistinguishable): ${groups}`);
  }
  const walk = (node: TreeNode, depth: number): void => {
    const indent = '  '.repeat(depth);
    if (node.kind === 'leaf') {
      const note =
        node.candidates.length > 1 ? '  (equivalence group: cannot be distinguished)' : '';
      lines.push(`${indent}* [${node.candidates.join(', ')}]${note}`);
      return;
    }
    lines.push(`${indent}? ${node.attribute} (cost ${node.attributeCost})`);
    for (const branch of node.branches) {
      lines.push(
        `${indent}  = ${JSON.stringify(branch.value)} -> candidates [${branch.candidates.join(', ')}]`,
      );
      walk(branch.child, depth + 2);
    }
  };
  walk(result.tree, 0);
  return lines.join('\n');
}

function validateProblem(problem: Problem): void {
  if (!problem || !Array.isArray(problem.attributes) || !Array.isArray(problem.candidates)) {
    throw new Error('Problem must contain "attributes" and "candidates" arrays.');
  }
  const n = problem.candidates.length;
  if (n < 1) throw new Error('At least one candidate is required.');
  if (n > MAX_CANDIDATES) {
    throw new Error(`At most ${MAX_CANDIDATES} candidates are supported (got ${n}).`);
  }
  const attrNames = new Set<string>();
  for (const attr of problem.attributes) {
    if (typeof attr.name !== 'string' || attr.name.length === 0) {
      throw new Error('Every attribute needs a non-empty string name.');
    }
    if (attrNames.has(attr.name)) throw new Error(`Duplicate attribute name: ${attr.name}`);
    attrNames.add(attr.name);
    if (typeof attr.cost !== 'number' || !Number.isFinite(attr.cost) || attr.cost < 0) {
      throw new Error(`Attribute "${attr.name}" needs a finite cost >= 0.`);
    }
  }
  const candNames = new Set<string>();
  for (const cand of problem.candidates) {
    if (typeof cand.name !== 'string' || cand.name.length === 0) {
      throw new Error('Every candidate needs a non-empty string name.');
    }
    if (candNames.has(cand.name)) throw new Error(`Duplicate candidate name: ${cand.name}`);
    candNames.add(cand.name);
    if (!cand.values || typeof cand.values !== 'object') {
      throw new Error(`Candidate "${cand.name}" needs a "values" object.`);
    }
    for (const attr of problem.attributes) {
      if (!(attr.name in cand.values)) {
        throw new Error(`Candidate "${cand.name}" is missing a value for attribute "${attr.name}".`);
      }
    }
  }
}
