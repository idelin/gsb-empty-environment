// Reference implementation: full recompute of the transitive closure from
// the edge multiset by BFS from every node. Used as the ground truth that
// the dynamic implementation is checked against.

export function buildAdjacency(edgeList) {
  const adj = new Map();
  const nodes = new Set();
  for (const [a, b] of edgeList) {
    nodes.add(a);
    nodes.add(b);
    let t = adj.get(a);
    if (!t) {
      t = new Map();
      adj.set(a, t);
    }
    t.set(b, (t.get(b) ?? 0) + 1);
  }
  return { adj, nodes };
}

// Returns Map<u, Set<v>> of all pairs (u, v) with a path of length >= 1,
// including (u, u) iff u lies on a directed cycle (self loops count).
export function fullClosure(edgeList) {
  const { adj, nodes } = buildAdjacency(edgeList);
  const pairs = new Map();
  for (const s of nodes) {
    const reach = new Set([s]);
    let selfCycle = false;
    const queue = [s];
    for (let i = 0; i < queue.length; i++) {
      const w = queue[i];
      const outs = adj.get(w);
      if (!outs) continue;
      for (const v of outs.keys()) {
        if (!reach.has(v)) {
          reach.add(v);
          queue.push(v);
        } else if (v === s) {
          selfCycle = true;
        }
      }
    }
    const set = new Set();
    for (const v of reach) if (v !== s) set.add(v);
    if (selfCycle) set.add(s);
    pairs.set(s, set);
  }
  return { pairs, adj, nodes };
}

// Checks that path is a concrete directed path in the multigraph: every
// consecutive pair must be an existing edge.
export function isValidPath(adj, path) {
  if (!Array.isArray(path) || path.length < 2) return false;
  for (let i = 0; i + 1 < path.length; i++) {
    const outs = adj.get(path[i]);
    if (!outs || !outs.has(path[i + 1])) return false;
  }
  return true;
}

// Map<u, Set<v>> <-> canonical string-key set helpers (nodes may be any
// JSON-serializable values in tests).
export function pairKey(u, v) {
  return JSON.stringify(u) + ' -> ' + JSON.stringify(v);
}

export function toKeySet(pairsMap) {
  const set = new Set();
  for (const [u, vs] of pairsMap) {
    for (const v of vs) set.add(pairKey(u, v));
  }
  return set;
}

export function pairListToMap(list) {
  const map = new Map();
  for (const [u, v] of list) {
    let s = map.get(u);
    if (!s) {
      s = new Set();
      map.set(u, s);
    }
    s.add(v);
  }
  return map;
}
