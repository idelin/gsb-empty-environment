'use strict';

/** Parse direct import/export-from specifiers, normalized to module ids. */
function parseImports(source) {
  const deps = [];
  const re = /(?:import|export)\s+(?:type\s+)?(?:[\w$*{}\s,]+\s+from\s+)?['"]([^'"]+)['"]/g;
  let m;
  while ((m = re.exec(source)) !== null) {
    deps.push(normalizeSpecifier(m[1]));
  }
  return deps;
}

function normalizeSpecifier(spec) {
  return spec.replace(/^\.\//, '').replace(/\.ts$/, '');
}

/** Build dependency graph: module id -> [direct dependency ids]. */
function buildGraph(sources) {
  const graph = new Map();
  for (const [id, source] of sources) {
    const deps = parseImports(source);
    for (const dep of deps) {
      if (!sources.has(dep)) {
        throw new Error(`module '${id}' imports unknown module '${dep}'`);
      }
    }
    graph.set(id, deps);
  }
  return graph;
}

/** Tarjan's strongly connected components. */
function tarjanSCC(graph) {
  const index = new Map();
  const lowlink = new Map();
  const onStack = new Set();
  const stack = [];
  const sccs = [];
  let counter = 0;

  function strongconnect(node) {
    index.set(node, counter);
    lowlink.set(node, counter);
    counter++;
    stack.push(node);
    onStack.add(node);

    for (const dep of graph.get(node)) {
      if (!index.has(dep)) {
        strongconnect(dep);
        lowlink.set(node, Math.min(lowlink.get(node), lowlink.get(dep)));
      } else if (onStack.has(dep)) {
        lowlink.set(node, Math.min(lowlink.get(node), index.get(dep)));
      }
    }

    if (lowlink.get(node) === index.get(node)) {
      const scc = [];
      let w;
      do {
        w = stack.pop();
        onStack.delete(w);
        scc.push(w);
      } while (w !== node);
      sccs.push(scc);
    }
  }

  for (const node of graph.keys()) {
    if (!index.has(node)) strongconnect(node);
  }
  return sccs;
}

/**
 * Order SCCs so that for every edge u -> v (u depends on v),
 * v's SCC comes before u's SCC (dependencies first).
 */
function topoSortSCCs(graph, sccs) {
  const sccOf = new Map();
  sccs.forEach((scc, i) => scc.forEach((id) => sccOf.set(id, i)));

  const indegree = new Array(sccs.length).fill(0);
  const dependents = new Map(); // sccIndex -> Set<sccIndex>
  for (const [id, deps] of graph) {
    for (const dep of deps) {
      const from = sccOf.get(dep); // dependency's SCC
      const to = sccOf.get(id);
      if (from === to) continue;
      if (!dependents.has(from)) dependents.set(from, new Set());
      if (!dependents.get(from).has(to)) {
        dependents.get(from).add(to);
        indegree[to]++;
      }
    }
  }

  const queue = [];
  for (let i = 0; i < sccs.length; i++) if (indegree[i] === 0) queue.push(i);
  const order = [];
  while (queue.length > 0) {
    const i = queue.shift();
    order.push(sccs[i]);
    for (const next of dependents.get(i) || []) {
      if (--indegree[next] === 0) queue.push(next);
    }
  }
  return order;
}

module.exports = { parseImports, buildGraph, tarjanSCC, topoSortSCCs };
