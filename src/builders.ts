// Small constructor helpers so types read like a DSL.
import type { Field, Param, Prim, Type } from "./types.js";

export function prim(p: Prim): Type {
  return { kind: "prim", prim: p };
}

export const num: Type = prim("number");
export const str: Type = prim("string");
export const bool: Type = prim("boolean");
export const nil: Type = prim("null");
export const undef: Type = prim("undefined");

export function rec(...fields: Field[]): Type {
  return { kind: "record", fields };
}

export function field(name: string, type: Type): Field {
  return { name, type, optional: false };
}

export function optField(name: string, type: Type): Field {
  return { name, type, optional: true };
}

export function union(...options: Type[]): Type {
  return { kind: "union", options };
}

export function func(params: Param[], ret: Type): Type {
  return { kind: "func", params, ret };
}

export function param(name: string, type: Type): Param {
  return { name, type, optional: false };
}

export function optParam(name: string, type: Type): Param {
  return { name, type, optional: true };
}

export function ref(name: string): Type {
  return { kind: "ref", name };
}
