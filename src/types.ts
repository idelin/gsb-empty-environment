export type Value = string | number | boolean | null;

export interface Fact {
  type: string;
  attrs: Record<string, Value>;
}

/** Working Memory Element: a fact asserted into the network. */
export interface WME {
  id: number;
  fact: Fact;
}

export type Op = "eq" | "ne" | "lt" | "lte" | "gt" | "gte";

/** A constant test applied to a single fact attribute (alpha network). */
export interface AlphaTest {
  field: string;
  op: Op;
  value: Value;
}

/**
 * Declarative condition of a rule.
 * - `type`  fact type to match
 * - `where` constant filters per attribute, either a literal (eq) or `{ op: value }`
 * - `bind`  maps an attribute to a variable name ("?x"); a variable reused in a
 *           later condition produces a beta join test
 */
export interface Condition {
  type: string;
  where?: Record<string, Value | Partial<Record<Op, Value>>>;
  bind?: Record<string, string>;
}

/** A join test: token binding of `varName` must equal `wme.fact.attrs[field]`. */
export interface JoinTest {
  varName: string;
  field: string;
}

/** A rule ready to be compiled into the network. */
export interface Rule {
  name: string;
  conditions: Condition[];
}

/** A satisfied rule instance sitting in the conflict set. */
export interface Activation {
  rule: string;
  bindings: Map<string, Value>;
  wmes: WME[];
}
