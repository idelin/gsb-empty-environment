export type Type = TVar | TCon | TFun | TList;

export interface TVar {
  kind: "var";
  id: number;
}

export interface TCon {
  kind: "con";
  name: string;
}

export interface TFun {
  kind: "fun";
  param: Type;
  result: Type;
}

export interface TList {
  kind: "list";
  element: Type;
}

export function tvar(id: number): TVar {
  return { kind: "var", id };
}

export function tcon(name: string): TCon {
  return { kind: "con", name };
}

export function tfun(param: Type, result: Type): TFun {
  return { kind: "fun", param, result };
}

export function tlist(element: Type): TList {
  return { kind: "list", element };
}

export const INT: TCon = tcon("Int");
export const BOOL: TCon = tcon("Bool");
export const STR: TCon = tcon("Str");

export function varName(id: number): string {
  let name = "";
  let n = id;
  do {
    name = String.fromCharCode(97 + (n % 26)) + name;
    n = Math.floor(n / 26) - 1;
  } while (n >= 0);
  return name;
}

export function showType(t: Type): string {
  if (t.kind === "var") return varName(t.id);
  if (t.kind === "con") return t.name;
  if (t.kind === "list") return "List<" + showType(t.element) + ">";
  return "(" + showType(t.param) + " -> " + showType(t.result) + ")";
}

export function typeEquals(a: Type, b: Type): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === "var" && b.kind === "var") return a.id === b.id;
  if (a.kind === "con" && b.kind === "con") return a.name === b.name;
  if (a.kind === "fun" && b.kind === "fun") {
    return typeEquals(a.param, b.param) && typeEquals(a.result, b.result);
  }
  if (a.kind === "list" && b.kind === "list") {
    return typeEquals(a.element, b.element);
  }
  return false;
}

export function occursVar(id: number, t: Type): boolean {
  if (t.kind === "var") return t.id === id;
  if (t.kind === "fun") return occursVar(id, t.param) || occursVar(id, t.result);
  if (t.kind === "list") return occursVar(id, t.element);
  return false;
}
