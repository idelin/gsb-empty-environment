/**
 * Randomized cross-check: the solver is compared against brute-force
 * enumeration of every possible install plan on small random inputs.
 */
import { strict as assert } from "assert";
import { solve } from "../src/solver";
import { satisfies, parseRange } from "../src/range";
import { DependencySpec, PackageDef, RequestSpec, SolverInput } from "../src/types";

function mulberry32(seed: number): () => number {
  return function () {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface Assignment {
  [name: string]: number | undefined;
}

/** Min-cost satisfying assignment by exhaustive enumeration, or null. */
function bruteForce(input: SolverInput): number | null {
  const repo = new Map<string, PackageDef>();
  for (const pkg of input.repository) repo.set(pkg.name, pkg);
  const installed = new Map<string, number>();
  if (input.installed && !Array.isArray(input.installed)) {
    for (const [k, v] of Object.entries(input.installed)) installed.set(k, v);
  } else if (Array.isArray(input.installed)) {
    for (const e of input.installed) installed.set(e.name, e.version);
  }
  const names = input.repository.map((p) => p.name);
  // choices[i] = version index + 1, or 0 for "not installed"
  const choices = names.map((n) => [0, ...repo.get(n)!.versions.map((_, i) => i + 1)]);
  let best: number | null = null;

  const inRange = (v: number, r?: string) => satisfies(v, parseRange(r));

  const check = (sel: number[]): number | null => {
    const assign: Assignment = {};
    names.forEach((n, i) => {
      assign[n] = sel[i] === 0 ? undefined : repo.get(n)!.versions[sel[i] - 1].version;
    });
    // Request constraints.
    for (const req of input.request) {
      const v = assign[req.name];
      if (v === undefined || !inRange(v, req.range)) return null;
    }
    // Enabled features: least fixed point.
    const feats = new Set<string>();
    for (const req of input.request) {
      for (const f of req.features ?? []) feats.add(`${req.name}:${f}`);
    }
    let changed = true;
    while (changed) {
      changed = false;
      const addFrom = (deps: DependencySpec[]) => {
        for (const d of deps) {
          for (const f of d.features ?? []) {
            const key = `${d.name}:${f}`;
            if (!feats.has(key)) {
              feats.add(key);
              changed = true;
            }
          }
        }
      };
      for (const pkg of input.repository) {
        const v = assign[pkg.name];
        if (v === undefined) continue;
        const pv = pkg.versions.find((x) => x.version === v)!;
        addFrom(pv.dependencies ?? []);
        for (const [fname, deps] of Object.entries(pv.features ?? {})) {
          if (feats.has(`${pkg.name}:${fname}`)) addFrom(deps);
        }
      }
    }
    // Dependency / feature / conflict constraints.
    for (const pkg of input.repository) {
      const v = assign[pkg.name];
      if (v === undefined) continue;
      const pv = pkg.versions.find((x) => x.version === v)!;
      const deps: DependencySpec[] = [...(pv.dependencies ?? [])];
      for (const [fname, fdeps] of Object.entries(pv.features ?? {})) {
        if (feats.has(`${pkg.name}:${fname}`)) deps.push(...fdeps);
      }
      for (const d of deps) {
        const tv = assign[d.name];
        if (tv === undefined || !inRange(tv, d.range)) return null;
      }
      for (const c of pv.conflicts ?? []) {
        const tv = assign[c.name];
        if (tv !== undefined && inRange(tv, c.range)) return null;
      }
    }
    // Cost: packages whose final state differs from the installed state.
    let cost = 0;
    const allNames = new Set([...names, ...installed.keys()]);
    for (const n of allNames) {
      if (assign[n] !== installed.get(n)) cost++;
    }
    return cost;
  };

  const walk = (i: number, sel: number[]): void => {
    if (i === names.length) {
      const cost = check(sel);
      if (cost !== null && (best === null || cost < best)) best = cost;
      return;
    }
    for (const c of choices[i]) {
      sel.push(c);
      walk(i + 1, sel);
      sel.pop();
    }
  };
  walk(0, []);
  return best;
}

function randomInput(rnd: () => number): SolverInput {
  const pick = <T>(arr: T[]): T => arr[Math.floor(rnd() * arr.length)];
  const nPkg = 2 + Math.floor(rnd() * 3); // 2..4 packages
  const names = Array.from({ length: nPkg }, (_, i) => `p${i}`);
  const featNames = ["x", "y"];
  const mkRange = (): string | undefined => {
    const r = rnd();
    if (r < 0.3) return undefined;
    if (r < 0.5) return `${1 + Math.floor(rnd() * 3)}`;
    if (r < 0.7) return `>=${1 + Math.floor(rnd() * 2)}`;
    if (r < 0.9) return `<${2 + Math.floor(rnd() * 2)}`;
    return `>=1 <=${1 + Math.floor(rnd() * 3)}`;
  };
  const mkDep = (): DependencySpec => {
    const d: DependencySpec = { name: pick(names), range: mkRange() };
    if (rnd() < 0.3) d.features = [pick(featNames)];
    return d;
  };
  const repository: PackageDef[] = names.map((name) => {
    const nVer = 1 + Math.floor(rnd() * 3); // 1..3 versions
    const versions = [];
    for (let v = 1; v <= nVer; v++) {
      const dependencies: DependencySpec[] = [];
      if (rnd() < 0.5) dependencies.push(mkDep());
      if (rnd() < 0.2) dependencies.push(mkDep());
      const features: Record<string, DependencySpec[]> = {};
      for (const f of featNames) {
        if (rnd() < 0.25) features[f] = [mkDep()];
      }
      const conflicts: DependencySpec[] = [];
      if (rnd() < 0.25) conflicts.push(mkDep());
      versions.push({ version: v, dependencies, features, conflicts });
    }
    return { name, versions };
  });
  const installed: Record<string, number> = {};
  for (const n of names) {
    if (rnd() < 0.5) installed[n] = 1 + Math.floor(rnd() * 3);
  }
  const request: RequestSpec[] = [];
  const nReq = 1 + Math.floor(rnd() * 3);
  for (let i = 0; i < nReq; i++) {
    const req: RequestSpec = { name: pick(names), range: mkRange() };
    if (rnd() < 0.3) req.features = [pick(featNames)];
    request.push(req);
  }
  return { repository, installed, request };
}

const rnd = mulberry32(20261005);
let checked = 0;
let conflicts = 0;
for (let iter = 0; iter < 400; iter++) {
  const input = randomInput(rnd);
  const expected = bruteForce(input);
  const result = solve(input);
  if (expected === null) {
    assert.equal(
      result.status,
      "conflict",
      `iter ${iter}: expected conflict, got ${JSON.stringify(result)}`,
    );
    if (result.status !== "conflict") break;
    conflicts++;
    // The core alone must already be unsatisfiable...
    const coreInput = { ...input, request: result.core };
    assert.equal(
      bruteForce(coreInput),
      null,
      `iter ${iter}: core is satisfiable: ${JSON.stringify(result.core)}`,
    );
    // ...and irreducible: every proper subset is satisfiable.
    for (let i = 0; i < result.core.length; i++) {
      const sub = { ...input, request: result.core.filter((_, j) => j !== i) };
      assert.notEqual(
        bruteForce(sub),
        null,
        `iter ${iter}: core not minimal without element ${i}`,
      );
    }
  } else {
    assert.equal(
      result.status,
      "ok",
      `iter ${iter}: expected plan with cost ${expected}, got ${JSON.stringify(result)}`,
    );
    if (result.status !== "ok") break;
    assert.equal(
      result.changes,
      expected,
      `iter ${iter}: cost ${result.changes} != optimal ${expected}`,
    );
  }
  checked++;
}
console.log(`ok - fuzz: ${checked} random problems cross-checked (${conflicts} conflicts)`);
