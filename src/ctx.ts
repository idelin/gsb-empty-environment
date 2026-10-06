/**
 * Type-checking context: constructor index, inhabitation and minimal
 * constructor depth per type.
 *
 * `minDepth(T)` is the depth of the smallest finite value of type `T`:
 *   minDepth(T) = 1 + min over ctors c of T of max over fields f of minDepth(type(f))
 * computed by monotone relaxation (Bellman-Ford style) over the finite type
 * graph, so recursive types are solved as a least fixed point instead of
 * being unfolded. A type with minDepth = Infinity has no finite values
 * (e.g. `type A = MkA A`) and is treated as uninhabited.
 */

import { CtorDef, TypeEnv, Value } from "./types";

export interface CtorInfo {
  typeName: string;
  def: CtorDef;
}

export class Ctx {
  readonly ctorIndex = new Map<string, CtorInfo>();
  readonly minDepth = new Map<string, number>();
  private readonly minValueCache = new Map<string, Value>();

  constructor(readonly env: TypeEnv) {
    for (const t of Object.values(env)) {
      for (const c of t.ctors) {
        const prev = this.ctorIndex.get(c.name);
        if (prev) {
          throw new Error(
            `constructor "${c.name}" is defined by both "${prev.typeName}" and "${t.name}"`
          );
        }
        this.ctorIndex.set(c.name, { typeName: t.name, def: c });
      }
    }
    for (const t of Object.values(env)) {
      for (const c of t.ctors) {
        for (const f of c.fields) {
          if (!env[f]) {
            throw new Error(`unknown type "${f}" in field of ${t.name}.${c.name}`);
          }
        }
      }
    }
    const names = Object.keys(env);
    for (const n of names) this.minDepth.set(n, Infinity);
    for (let round = 0; round <= names.length; round++) {
      let changed = false;
      for (const t of Object.values(env)) {
        let best = Infinity;
        for (const c of t.ctors) {
          let worst = 0;
          for (const f of c.fields) worst = Math.max(worst, this.minDepth.get(f)!);
          if (worst !== Infinity) best = Math.min(best, 1 + worst);
        }
        if (best < this.minDepth.get(t.name)!) {
          this.minDepth.set(t.name, best);
          changed = true;
        }
      }
      if (!changed) break;
    }
  }

  ctorInfo(name: string): CtorInfo {
    const info = this.ctorIndex.get(name);
    if (!info) throw new Error(`unknown constructor "${name}"`);
    return info;
  }

  inhabited(typeName: string): boolean {
    return this.minDepth.get(typeName)! < Infinity;
  }

  /** A finite value of the given type with minimal constructor depth. */
  minValue(typeName: string): Value {
    const cached = this.minValueCache.get(typeName);
    if (cached) return cached;
    const t = this.env[typeName];
    if (!t) throw new Error(`unknown type "${typeName}"`);
    let best: CtorDef | null = null;
    let bestDepth = Infinity;
    for (const c of t.ctors) {
      let worst = 0;
      for (const f of c.fields) worst = Math.max(worst, this.minDepth.get(f)!);
      const d = worst === Infinity ? Infinity : 1 + worst;
      if (d < bestDepth) {
        bestDepth = d;
        best = c;
      }
    }
    if (!best) throw new Error(`type "${typeName}" has no finite values`);
    const v: Value = { ctor: best.name, args: best.fields.map((f) => this.minValue(f)) };
    this.minValueCache.set(typeName, v);
    return v;
  }
}
