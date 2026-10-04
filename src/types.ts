// ---------------------------------------------------------------------------
// Type system: a finite structural type system with records, unions and
// functions. Recursive types are expressed indirectly through named
// references (`ref`) resolved against an environment (`Env`).
// ---------------------------------------------------------------------------

export type Prim = "number" | "string" | "boolean" | "null" | "undefined";

export type Type =
  | { kind: "prim"; prim: Prim }
  | { kind: "record"; fields: Field[] }
  | { kind: "union"; options: Type[] }
  | { kind: "func"; params: Param[]; ret: Type }
  | { kind: "ref"; name: string };

export interface Field {
  name: string;
  type: Type;
  // Optional (`a?: T`) is NOT the same as nullable (`a: T | undefined`):
  // an optional key may be absent entirely, a nullable key is always present.
  optional: boolean;
}

export interface Param {
  name?: string;
  type: Type;
  optional?: boolean;
}

// Named (possibly recursive) type definitions, e.g.
//   List = { head: number, tail: List | null }
export type Env = ReadonlyMap<string, Type>;

// ---------------------------------------------------------------------------
// Counterexample value shapes: a minimal sketch of a value that belongs to
// the new interface but is not accepted by the old one.
// ---------------------------------------------------------------------------

export type Shape =
  | { kind: "lit"; text: string } // 0, "s", true, null, undefined
  | { kind: "record"; fields: FieldShape[] }
  | { kind: "func"; ret: Shape } // (…) => <ret>
  | { kind: "arg"; shape: Shape } // a call argument, not a field value
  | { kind: "cycle"; name: string }; // recursion marker …Name

export interface FieldShape {
  name: string;
  shape: Shape;
}

export interface Incompatibility {
  path: string; // e.g. $.user.handle.arg0(e).code
  message: string;
  witness: Shape;
}

export interface CheckResult {
  compatible: boolean;
  problems: Incompatibility[];
}
