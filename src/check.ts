import type { CheckResult, Env, Incompatibility, Shape, Type } from "./types.js";
import { resolve } from "./resolve.js";
import { sampleOf, sampleRecordWith } from "./sample.js";

interface Ctx {
  env: Env;
  // Pairs of type nodes currently known/assumed compatible, keyed by node
  // identity. This is the "pairwise visited state" that makes the
  // comparison of recursive types terminate (coinductive, Amadio–Cardelli
  // style): re-encountering a pair means we are in a cycle, and any real
  // violation would already have been found while the pair was explored.
  seen: Set<string>;
  ids: Map<Type, number>;
  nextId: number;
}

// Can `newType` replace `oldType`? Holds when new <: old, i.e. every value
// produced through the new interface is accepted where the old one was used.
export function checkReplacement(newType: Type, oldType: Type, env: Env): CheckResult {
  const ctx: Ctx = { env, seen: new Set(), ids: new Map(), nextId: 0 };
  const problems = check(newType, oldType, ctx, "$");
  return { compatible: problems.length === 0, problems };
}

function idOf(t: Type, ctx: Ctx): number {
  const hit = ctx.ids.get(t);
  if (hit !== undefined) return hit;
  const id = ctx.nextId;
  ctx.nextId += 1;
  ctx.ids.set(t, id);
  return id;
}

function fail(path: string, message: string, witness: Shape): Incompatibility[] {
  return [{ path, message, witness }];
}

function describe(t: Type): string {
  switch (t.kind) {
    case "prim": return "primitive " + t.prim;
    case "record": return "record";
    case "union": return "union";
    case "func": return "function";
    case "ref": return "reference " + t.name;
  }
}

// Checks l <: r and returns every incompatibility found below `path`.
// Each returned witness is a value of l that r does not accept.
function check(left: Type, right: Type, ctx: Ctx, path: string): Incompatibility[] {
  const l = resolve(left, ctx.env);
  const r = resolve(right, ctx.env);

  const key = idOf(l, ctx) + "<=" + idOf(r, ctx);
  if (ctx.seen.has(key)) return [];
  ctx.seen.add(key);

  if (l.kind === "prim" && r.kind === "prim") {
    if (l.prim === r.prim) return [];
    return fail(path, "primitive " + l.prim + " is not assignable to " + r.prim, sampleOf(left, ctx.env));
  }

  // Union on the left: every option must be assignable to the right.
  if (l.kind === "union") {
    const problems: Incompatibility[] = [];
    for (let i = 0; i < l.options.length; i += 1) {
      problems.push(...check(l.options[i], r, ctx, path + "|#" + i));
    }
    return problems;
  }

  // Union on the right: the left must fit at least one branch. Failed
  // branch trials are rolled back so their partial assumptions cannot
  // leak into sibling branches.
  if (r.kind === "union") {
    for (const opt of r.options) {
      const snapshot: Set<string> = new Set(ctx.seen);
      const trial = check(l, opt, ctx, path);
      if (trial.length === 0) return [];
      ctx.seen = snapshot;
    }
    return fail(path, "matches none of the " + r.options.length + " union branches", sampleOf(left, ctx.env));
  }

  if (l.kind === "record" && r.kind === "record") {
    const problems: Incompatibility[] = [];
    for (const rf of r.fields) {
      const lf = l.fields.find((f) => f.name === rf.name);
      const fieldPath = path + "." + rf.name;
      if (lf === undefined) {
        if (!rf.optional) {
          problems.push(...fail(
            fieldPath,
            "old interface requires field \"" + rf.name + "\", but the new interface does not provide it",
            sampleOf(left, ctx.env),
          ));
        }
        continue;
      }
      if (!rf.optional && lf.optional) {
        // Optional is not nullable: the new interface may omit a key the
        // old interface requires to be present.
        problems.push(...fail(
          fieldPath,
          "old interface requires field \"" + rf.name + "\", but the new interface marks it optional (optional != nullable)",
          sampleOf(left, ctx.env),
        ));
        continue;
      }
      // Field types are covariant. Extra fields on the left are fine (width).
      const sub = check(lf.type, rf.type, ctx, fieldPath);
      for (const p of sub) {
        problems.push({
          path: p.path,
          message: p.message,
          witness: sampleRecordWith(left, ctx.env, rf.name, p.witness),
        });
      }
    }
    return problems;
  }

  if (l.kind === "func" && r.kind === "func") {
    const problems: Incompatibility[] = [];
    for (let i = 0; i < l.params.length; i += 1) {
      const lp = l.params[i];
      const rp = i < r.params.length ? r.params[i] : undefined;
      const label = lp.name ?? "arg" + i;
      const argPath = path + ".arg" + i + "(" + label + ")";
      if (rp === undefined) {
        // A new function may accept FEWER parameters than the old one, but
        // never require more than old-interface callers supply.
        if (!lp.optional) {
          problems.push(...fail(
            argPath,
            "new function requires parameter #" + i + " (\"" + label + "\"), which old-interface callers never supply",
            { kind: "arg", shape: sampleOf(lp.type, ctx.env) },
          ));
        }
        continue;
      }
      if (!lp.optional && rp.optional === true) {
        problems.push(...fail(
          argPath,
          "old-interface callers may omit argument #" + i + " (\"" + label + "\"), but the new function requires it",
          { kind: "arg", shape: sampleOf(lp.type, ctx.env) },
        ));
        continue;
      }
      // Parameters are contravariant: old parameter type <: new parameter type.
      // The witness is an argument value old callers may pass that the new
      // function cannot accept.
      const argProblems = check(rp.type, lp.type, ctx, argPath);
      for (const p of argProblems) {
        problems.push({ path: p.path, message: p.message, witness: { kind: "arg", shape: p.witness } });
      }
    }
    // Return types are covariant.
    const retProblems = check(l.ret, r.ret, ctx, path + ".return");
    for (const p of retProblems) {
      problems.push({ path: p.path, message: p.message, witness: { kind: "func", ret: p.witness } });
    }
    return problems;
  }

  return fail(path, describe(l) + " is not assignable to " + describe(r), sampleOf(left, ctx.env));
}
