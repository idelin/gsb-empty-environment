import type { Env, FieldShape, Shape, Type } from "./types.js";

// Builds a minimal value shape for a type:
// - records contain only their required fields,
// - unions pick the first option that does not immediately recurse,
// - recursive references collapse to a …Name cycle marker.
export function sampleOf(t: Type, env: Env): Shape {
  return sample(t, env, new Set());
}

function sample(t: Type, env: Env, seen: Set<string>): Shape {
  if (t.kind === "ref") {
    if (seen.has(t.name)) return { kind: "cycle", name: t.name };
    const next = env.get(t.name);
    if (next === undefined) {
      throw new Error(`unknown type reference: ${t.name}`);
    }
    const nextSeen: Set<string> = new Set(seen);
    nextSeen.add(t.name);
    return sample(next, env, nextSeen);
  }
  switch (t.kind) {
    case "prim":
      return { kind: "lit", text: primSample(t.prim) };
    case "record": {
      const fields: FieldShape[] = [];
      for (const f of t.fields) {
        if (f.optional) continue;
        fields.push({ name: f.name, shape: sample(f.type, env, new Set(seen)) });
      }
      return { kind: "record", fields };
    }
    case "union": {
      let fallback: Shape | undefined;
      for (const opt of t.options) {
        const s = sample(opt, env, new Set(seen));
        if (s.kind !== "cycle") return s;
        if (fallback === undefined) fallback = s;
      }
      if (fallback !== undefined) return fallback;
      throw new Error("cannot sample an empty union");
    }
    case "func":
      return { kind: "func", ret: sample(t.ret, env, new Set(seen)) };
  }
}

function primSample(p: string): string {
  switch (p) {
    case "number": return "0";
    case "string": return "\"s\"";
    case "boolean": return "true";
    case "null": return "null";
    default: return "undefined";
  }
}

// Minimal sample of `recordType` where one field is replaced by an already
// computed (failing) witness, so the counterexample stays a full value of
// the new interface. Argument witnesses describe a call, not a field value,
// so they pass through untouched. Ref names on the way in seed the cycle
// detection, keeping recursive sibling fields collapsed.
export function sampleRecordWith(recordType: Type, env: Env, field: string, fieldShape: Shape): Shape {
  if (fieldShape.kind === "arg") return fieldShape;
  const seed: Set<string> = new Set();
  let cur = recordType;
  while (cur.kind === "ref") {
    seed.add(cur.name);
    const next = env.get(cur.name);
    if (next === undefined) break;
    cur = next;
  }
  if (cur.kind !== "record") return fieldShape;
  const fields: FieldShape[] = [];
  for (const f of cur.fields) {
    if (f.optional) continue;
    fields.push({ name: f.name, shape: f.name === field ? fieldShape : sample(f.type, env, new Set(seed)) });
  }
  return { kind: "record", fields };
}
