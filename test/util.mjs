export function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function randInt(rand, lo, hi) {
  return lo + Math.floor(rand() * (hi - lo + 1));
}

export function shuffled(arr, rand) {
  const a = arr.slice();
  for (let i = a.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    const tmp = a[i];
    a[i] = a[j];
    a[j] = tmp;
  }
  return a;
}

export function makeInstance(rand, options = {}) {
  const maxRequests = options.maxRequests ?? 8;
  const maxNodes = options.maxNodes ?? 3;
  const regionPool = ['cn-north', 'cn-east', 'cn-south'];
  const regions = regionPool.slice(0, randInt(rand, 2, 3));

  const nodes = [];
  const nodeCount = randInt(rand, 1, maxNodes);
  for (let k = 0; k < nodeCount; k++) {
    nodes.push({
      id: `n${k}`,
      region: regions[randInt(rand, 0, regions.length - 1)],
      capacity: randInt(rand, 0, 15),
    });
  }

  const requests = [];
  const reqCount = randInt(rand, 1, maxRequests);
  for (let k = 0; k < reqCount; k++) {
    requests.push({
      id: `r${k}`,
      region: regions[randInt(rand, 0, regions.length - 1)],
      demand: randInt(rand, 1, 9),
    });
  }

  const costs = {};
  for (const a of regions) {
    costs[a] = {};
    for (const b of regions) {
      if (a !== b) costs[a][b] = randInt(rand, 0, 5);
    }
  }

  const previous = {};
  const ghostNodes = ['ghost-1', 'ghost-2'];
  for (const r of requests) {
    if (rand() < 0.5) {
      const pool = nodes.map((n) => n.id).concat(ghostNodes, [null]);
      previous[r.id] = pool[randInt(rand, 0, pool.length - 1)];
    }
  }

  return { requests, nodes, costs, previous };
}

export function projection(result) {
  return JSON.stringify({
    assignments: result.assignments,
    rejected: result.rejected,
    capacityLedger: result.capacityLedger,
    migrations: result.migrations,
    admittedDemand: result.stats.admittedDemand,
    crossRegionCost: result.stats.crossRegionCost,
    migratedCount: result.stats.migratedCount,
  });
}

export function assertFeasible(result, input) {
  const byId = new Map(input.nodes.map((n) => [n.id, n]));
  const used = new Map(input.nodes.map((n) => [n.id, 0]));
  for (const r of input.requests) {
    const nodeId = result.assignments[r.id];
    if (nodeId === null || nodeId === undefined) continue;
    if (!byId.has(nodeId)) throw new Error(`assigned to unknown node ${nodeId}`);
    used.set(nodeId, used.get(nodeId) + r.demand);
  }
  for (const entry of result.capacityLedger) {
    const node = byId.get(entry.nodeId);
    if (used.get(entry.nodeId) > node.capacity) {
      throw new Error(`node ${entry.nodeId} over capacity`);
    }
    if (entry.used !== used.get(entry.nodeId)) {
      throw new Error(`ledger mismatch on ${entry.nodeId}`);
    }
    if (entry.remaining !== entry.capacity - entry.used) {
      throw new Error(`ledger remaining mismatch on ${entry.nodeId}`);
    }
    if (entry.remaining < 0) throw new Error(`negative remaining on ${entry.nodeId}`);
  }
  const rejectedSet = new Set(result.rejected);
  for (const r of input.requests) {
    const assigned = result.assignments[r.id];
    if ((assigned === null) !== rejectedSet.has(r.id)) {
      throw new Error(`rejected list inconsistent for ${r.id}`);
    }
  }
}
