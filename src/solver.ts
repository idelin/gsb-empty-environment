import { SatSolver, addAtMost } from "./sat";
import { filterVersions } from "./range";
import {
  DependencySpec,
  PackageDef,
  PlanConflict,
  PlanEntry,
  PlanOk,
  RequestSpec,
  SolveResult,
  SolverInput,
} from "./types";

const MAX_PACKAGES = 100; // designed for <= 20; hard cap guards against blowups

interface Encoded {
  sat: SatSolver;
  packages: string[];
  versionsOf: Map<string, number[]>;
  versionVar: Map<string, number>; // "name@version" -> var
  featureVar: Map<string, number>; // "name:feature" -> var
  requestLits: number[]; // assumption literal per request constraint
  costVarOf: Map<string, number>;
  installed: Map<string, number>;
}

function vkey(name: string, version: number): string {
  return `${name}@${version}`;
}
function fkey(name: string, feature: string): string {
  return `${name}:${feature}`;
}

function normalizeInstalled(
  installed: SolverInput["installed"],
): Map<string, number> {
  const map = new Map<string, number>();
  if (!installed) return map;
  if (Array.isArray(installed)) {
    for (const e of installed) map.set(e.name, e.version);
  } else {
    for (const [name, version] of Object.entries(installed)) map.set(name, version);
  }
  return map;
}

function validate(input: SolverInput): void {
  if (!Array.isArray(input.repository)) throw new Error("repository must be an array");
  if (!Array.isArray(input.request)) throw new Error("request must be an array");
  if (input.repository.length > MAX_PACKAGES) {
    throw new Error(`too many packages (>${MAX_PACKAGES})`);
  }
  const seen = new Set<string>();
  for (const pkg of input.repository) {
    if (seen.has(pkg.name)) throw new Error(`duplicate package: ${pkg.name}`);
    seen.add(pkg.name);
    const vers = new Set<number>();
    for (const v of pkg.versions) {
      if (!Number.isInteger(v.version)) {
        throw new Error(`${pkg.name}: version ${v.version} is not an integer`);
      }
      if (vers.has(v.version)) {
        throw new Error(`${pkg.name}: duplicate version ${v.version}`);
      }
      vers.add(v.version);
    }
  }
}

function encode(input: SolverInput): Encoded {
  validate(input);
  const sat = new SatSolver();
  const repo = new Map<string, PackageDef>();
  for (const pkg of input.repository) repo.set(pkg.name, pkg);
  const installed = normalizeInstalled(input.installed);

  const packages = input.repository.map((p) => p.name);
  const versionsOf = new Map<string, number[]>();
  const versionVar = new Map<string, number>();
  const featureVar = new Map<string, number>();

  const varForVersion = (name: string, version: number): number => {
    const key = vkey(name, version);
    let v = versionVar.get(key);
    if (v === undefined) {
      v = sat.newVar();
      versionVar.set(key, v);
    }
    return v;
  };
  const varForFeature = (name: string, feature: string): number => {
    const key = fkey(name, feature);
    let v = featureVar.get(key);
    if (v === undefined) {
      v = sat.newVar();
      featureVar.set(key, v);
    }
    return v;
  };

  // Version variables + at-most-one-version per package.
  for (const pkg of input.repository) {
    const vers = pkg.versions.map((v) => v.version).sort((a, b) => a - b);
    versionsOf.set(pkg.name, vers);
    const vars = vers.map((v) => varForVersion(pkg.name, v));
    for (let i = 0; i < vars.length; i++) {
      for (let j = i + 1; j < vars.length; j++) {
        sat.addClause([-vars[i], -vars[j]]);
      }
    }
  }

  const depClause = (
    fromLits: number[],
    dep: DependencySpec,
  ): void => {
    const target = repo.get(dep.name);
    const vers = target ? filterVersions(versionsOf.get(dep.name)!, dep.range) : [];
    const clause = [...fromLits.map((l) => -l)];
    for (const v of vers) clause.push(varForVersion(dep.name, v));
    sat.addClause(clause);
    for (const feat of dep.features ?? []) {
      sat.addClause([...fromLits.map((l) => -l), varForFeature(dep.name, feat)]);
    }
  };

  // Dependencies, optional features, conflicts.
  for (const pkg of input.repository) {
    for (const pv of pkg.versions) {
      const xv = varForVersion(pkg.name, pv.version);
      for (const dep of pv.dependencies ?? []) depClause([xv], dep);
      for (const [feat, deps] of Object.entries(pv.features ?? {})) {
        const fv = varForFeature(pkg.name, feat);
        for (const dep of deps) depClause([fv, xv], dep);
      }
      for (const c of pv.conflicts ?? []) {
        const target = repo.get(c.name);
        if (!target) continue; // conflicting with an absent package is vacuous
        const bad = filterVersions(versionsOf.get(c.name)!, c.range);
        for (const w of bad) {
          sat.addClause([-xv, -varForVersion(c.name, w)]);
        }
      }
    }
  }

  // A feature may only be on when its package is installed.
  for (const [key, fv] of featureVar) {
    const name = key.slice(0, key.indexOf(":"));
    const vers = versionsOf.get(name);
    if (!vers) continue;
    sat.addClause([-fv, ...vers.map((v) => varForVersion(name, v))]);
  }

  // Request constraints, each guarded by an assumption literal so that an
  // irreducible conflict subset can be extracted on failure.
  const requestLits: number[] = [];
  for (const req of input.request) {
    const a = sat.newVar();
    requestLits.push(a);
    const vers = versionsOf.get(req.name);
    const ok = vers ? filterVersions(vers, req.range) : [];
    sat.addClause([-a, ...ok.map((v) => varForVersion(req.name, v))]);
    for (const feat of req.features ?? []) {
      sat.addClause([-a, varForFeature(req.name, feat)]);
    }
  }

  // Cost variables: one per package that is installed or installable.
  // c_p is forced true whenever the final state differs from the installed one.
  const costVarOf = new Map<string, number>();
  const costNames = new Set<string>([...packages, ...installed.keys()]);
  for (const name of costNames) {
    const c = sat.newVar();
    costVarOf.set(name, c);
    const iv = installed.get(name);
    const vers = versionsOf.get(name) ?? [];
    if (iv !== undefined && vers.includes(iv)) {
      // Keeping the installed version is the only zero-cost outcome.
      sat.addClause([varForVersion(name, iv), c]);
    } else if (iv !== undefined) {
      // Installed version is not in the repository: any outcome is a change.
      sat.addClause([c]);
    } else {
      // Not installed: selecting any version costs one change.
      for (const v of vers) sat.addClause([-varForVersion(name, v), c]);
    }
  }

  return {
    sat,
    packages,
    versionsOf,
    versionVar,
    featureVar,
    requestLits,
    costVarOf,
    installed,
  };
}

function extractPlan(enc: Encoded, model: boolean[], input: SolverInput): PlanOk {
  const plan: PlanEntry[] = [];
  const removed: string[] = [];
  let changes = 0;
  for (const name of enc.packages) {
    const vers = enc.versionsOf.get(name)!;
    const chosen = vers.find((v) => model[enc.versionVar.get(vkey(name, v))!]);
    if (chosen === undefined) continue;
    const features: string[] = [];
    for (const [key, fv] of enc.featureVar) {
      const sep = key.indexOf(":");
      if (key.slice(0, sep) === name && model[fv]) features.push(key.slice(sep + 1));
    }
    features.sort();
    const iv = enc.installed.get(name);
    let change: PlanEntry["change"];
    if (iv === undefined) change = "new";
    else if (iv === chosen) change = "keep";
    else change = chosen > iv ? "upgrade" : "downgrade";
    if (change !== "keep") changes++;
    plan.push({ name, version: chosen, features, change });
  }
  for (const name of enc.installed.keys()) {
    if (!plan.some((e) => e.name === name)) {
      removed.push(name);
      changes++;
    }
  }
  plan.sort((a, b) => a.name.localeCompare(b.name));
  removed.sort();
  return { status: "ok", plan, removed, changes };
}

function explainConflict(input: SolverInput, core: RequestSpec[]): string[] {
  const lines: string[] = [];
  const repo = new Map<string, PackageDef>();
  for (const pkg of input.repository) repo.set(pkg.name, pkg);
  const installed = normalizeInstalled(input.installed);
  const involved = new Set<string>();
  for (const req of core) {
    const feats = req.features?.length ? ` (features: ${req.features.join(", ")})` : "";
    lines.push(`require ${req.name} ${req.range ?? "*"}${feats}`);
    involved.add(req.name);
  }
  for (const name of [...involved].sort()) {
    const pkg = repo.get(name);
    const iv = installed.get(name);
    if (iv !== undefined) lines.push(`installed: ${name}@${iv}`);
    if (!pkg) {
      lines.push(`${name}: not found in repository`);
      continue;
    }
    const vers = pkg.versions.map((v) => v.version).sort((a, b) => a - b);
    lines.push(`${name}: available versions ${vers.join(", ")}`);
    for (const pv of [...pkg.versions].sort((a, b) => a.version - b.version)) {
      for (const dep of pv.dependencies ?? []) {
        const feats = dep.features?.length ? ` (features: ${dep.features.join(", ")})` : "";
        lines.push(`${name}@${pv.version} depends on ${dep.name} ${dep.range ?? "*"}${feats}`);
      }
      for (const [feat, deps] of Object.entries(pv.features ?? {})) {
        for (const dep of deps) {
          lines.push(`${name}@${pv.version} [feature ${feat}] depends on ${dep.name} ${dep.range ?? "*"}`);
        }
      }
      for (const c of pv.conflicts ?? []) {
        lines.push(`${name}@${pv.version} conflicts with ${c.name} ${c.range ?? "*"}`);
      }
    }
  }
  return lines;
}

/**
 * Solve the install problem.
 *
 * Objective: minimize the number of packages whose final state differs from
 * the installed state (upgrade / downgrade / new install / removal each count
 * as one change). Ties are broken by preferring higher versions, processed
 * lexicographically: requested packages first (in request order), then the
 * remaining packages by name.
 */
export function solve(input: SolverInput): SolveResult {
  const enc = encode(input);
  const { sat, requestLits } = enc;
  const debug = process.env.PKGSOLVER_DEBUG === "1";
  const t0 = Date.now();

  if (sat.solve(requestLits) === null) {
    // Deletion-based minimization: drop every request constraint that is not
    // needed to keep the subset unsatisfiable. The result is irreducible.
    let coreIdx = requestLits.map((_, i) => i);
    for (let i = 0; i < coreIdx.length; ) {
      const trial = coreIdx.filter((_, j) => j !== i);
      if (sat.solve(trial.map((idx) => requestLits[idx])) === null) {
        coreIdx = trial;
      } else {
        i++;
      }
    }
    const core = coreIdx.map((idx) => input.request[idx]);
    const result: PlanConflict = {
      status: "conflict",
      core,
      explanation: explainConflict(input, core),
    };
    return result;
  }

  // Minimize the number of changes with a guarded cardinality constraint.
  const costVars = [...enc.costVarOf.values()];
  let act = -1;
  let model: boolean[] | null = null;
  for (let k = 0; k <= costVars.length; k++) {
    act = sat.newVar();
    addAtMost(sat, costVars, k, act);
    model = sat.solve([...requestLits, act]);
    if (debug) console.error(`cost k=${k}: ${model ? "sat" : "unsat"} (+${Date.now() - t0}ms)`);
    if (model !== null) break;
  }
  if (model === null) {
    // Unreachable: the base problem is satisfiable and k = n is vacuous.
    throw new Error("internal error: optimization pass failed");
  }

  // Tie-break: lexicographically push versions as high as possible.
  const order = [
    ...input.request.map((r) => r.name),
    ...enc.packages.filter((p) => !input.request.some((r) => r.name === p)).sort(),
  ];
  const assumptions = [...requestLits, act];
  for (const name of order) {
    const vers = enc.versionsOf.get(name);
    if (!vers) continue;
    for (const v of [...vers].sort((a, b) => b - a)) {
      const lit = enc.versionVar.get(vkey(name, v))!;
      const t1 = Date.now();
      const trial = sat.solve([...assumptions, lit]);
      if (debug) console.error(`greedy ${name}@${v}: ${trial ? "sat" : "unsat"} (${Date.now() - t1}ms)`);
      if (trial !== null) {
        assumptions.push(lit);
        model = trial;
        break;
      }
    }
  }

  return extractPlan(enc, model as boolean[], input);
}
