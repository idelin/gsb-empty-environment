/**
 * Type language for the unification engine.
 *
 *   t ::= var        (unification variable)
 *       | con        (ground constructor, e.g. Int, Bool)
 *       | t -> t     (function type)
 *       | List t     (list type)
 */

export interface TVar {
  readonly kind: "var";
  readonly id: number;
}

export interface TCon {
  readonly kind: "con";
  readonly name: string;
}

export interface TFun {
  readonly kind: "fun";
  readonly arg: Type;
  readonly ret: Type;
}

export interface TList {
  readonly kind: "list";
  readonly elem: Type;
}

export type Type = TVar | TCon | TFun | TList;

export const tvar = (id: number): TVar => ({ kind: "var", id });
export const tcon = (name: string): TCon => ({ kind: "con", name });
export const tfun = (arg: Type, ret: Type): TFun => ({ kind: "fun", arg, ret });
export const tlist = (elem: Type): TList => ({ kind: "list", elem });

/** Structural equality (types must already be substitution-normalised). */
export function typeEq(a: Type, b: Type): boolean {
  if (a.kind !== b.kind) return false;
  switch (a.kind) {
    case "var":
      return a.id === (b as TVar).id;
    case "con":
      return a.name === (b as TCon).name;
    case "fun": {
      const bf = b as TFun;
      return typeEq(a.arg, bf.arg) && typeEq(a.ret, bf.ret);
    }
    case "list":
      return typeEq(a.elem, (b as TList).elem);
  }
}

const VAR_NAMES = "abcdefghijklmnopqrstuvwxyz";

export function varName(id: number): string {
  return id < VAR_NAMES.length ? VAR_NAMES[id]! : `t${id}`;
}

export function showType(t: Type): string {
  switch (t.kind) {
    case "var":
      return varName(t.id);
    case "con":
      return t.name;
    case "fun": {
      const arg = t.arg.kind === "fun" ? `(${showType(t.arg)})` : showType(t.arg);
      return `${arg} -> ${showType(t.ret)}`;
    }
    case "list":
      return `List<${showType(t.elem)}>`;
  }
}

/** All variable ids occurring in a type (no substitution expansion). */
export function freeVars(t: Type, out: Set<number> = new Set()): Set<number> {
  switch (t.kind) {
    case "var":
      out.add(t.id);
      break;
    case "fun":
      freeVars(t.arg, out);
      freeVars(t.ret, out);
      break;
    case "list":
      freeVars(t.elem, out);
      break;
    case "con":
      break;
  }
  return out;
}
