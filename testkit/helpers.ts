import { ipv4ToInt } from "../src/index.ts";
import type {
  Action,
  Condition,
  Direction,
  Policy,
  Rule,
  Side,
} from "../src/index.ts";

export const ip = ipv4ToInt;

export const prefix = (side: Side, addr: string, length: number): Condition => ({
  kind: "prefix",
  side,
  prefix: ip(addr),
  length,
});

export const port = (side: Side, min: number, max: number): Condition => ({
  kind: "port",
  side,
  min,
  max,
});

export const dir = (direction: Direction): Condition => ({ kind: "direction", direction });
export const fwd: Condition = dir("forward");
export const rev: Condition = dir("reverse");

/** 三位标签掩码条件：(label & mask) === value。 */
export const labelIs = (mask: number, value: number): Condition => ({
  kind: "label",
  mask,
  value,
});

export const and = (...all: Condition[]): Condition => ({ kind: "and", all });
export const or = (...any: Condition[]): Condition => ({ kind: "or", any });
export const not = (cond: Condition): Condition => ({ kind: "not", cond });

/** 恒真条件：方向非此即彼。 */
export const ANY: Condition = or(fwd, rev);

export const allow = (setLabel?: number): Action => ({ allow: true, setLabel });
export const deny = (): Action => ({ allow: false });

export const rule = (priority: number, when: Condition, action: Action): Rule => ({
  priority,
  when,
  action,
});

export const policy = (...rules: Rule[]): Policy => ({ rules });
