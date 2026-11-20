/**
 * 有状态过滤策略的数据模型与求值语义。
 *
 * 策略 = 若干带优先级的规则；最高优先级先匹配，同优先级按声明顺序。
 * 条件可检查地址前缀、端口区间、方向、连接标签的三位掩码，以及与/或/非组合。
 * 动作 = 允许或拒绝，允许时可设置新标签；拒绝不改变标签。
 * 连接按五元组及其反向识别，首次标签为 0；无规则匹配时默认拒绝。
 */

export const MAX_RULES = 40;
export const LABEL_BITS = 3;
export const LABEL_COUNT = 1 << LABEL_BITS;
export const MAX_PORT = 65535;
export const MAX_PREFIX_LEN = 32;
export const MAX_ADDR = 0xffffffff;

export type Direction = "forward" | "reverse";
export type Side = "src" | "dst";

export interface PrefixCond {
  kind: "prefix";
  side: Side;
  /** 网络地址（主机位会被忽略，按 length 归一化）。 */
  prefix: number;
  /** 前缀长度 0..32。 */
  length: number;
}
export interface PortCond {
  kind: "port";
  side: Side;
  min: number;
  max: number;
}
export interface DirCond {
  kind: "direction";
  direction: Direction;
}
/** 三位标签掩码条件：当 (label & mask) === value 时匹配。 */
export interface LabelCond {
  kind: "label";
  mask: number;
  value: number;
}
export interface AndCond {
  kind: "and";
  all: Condition[];
}
export interface OrCond {
  kind: "or";
  any: Condition[];
}
export interface NotCond {
  kind: "not";
  cond: Condition;
}
export type Condition =
  | PrefixCond
  | PortCond
  | DirCond
  | LabelCond
  | AndCond
  | OrCond
  | NotCond;

export interface Action {
  allow: boolean;
  /** 仅允许动作可设置；拒绝动作不得携带（拒绝不改变标签）。 */
  setLabel?: number;
}

export interface Rule {
  priority: number;
  when: Condition;
  action: Action;
}

export interface Policy {
  rules: Rule[];
}

/** 一条连接上的单向报文抽象：地址/端口为 32 位与 0..65535 的整数。 */
export interface Packet {
  src: number;
  dst: number;
  srcPort: number;
  dstPort: number;
  direction: Direction;
}

export interface EvalResult {
  /** 命中的规则声明序号；无匹配（默认拒绝）时为 -1。 */
  rule: number;
  allow: boolean;
  /** 处理后的标签（拒绝时等于入参标签）。 */
  label: number;
}

export interface CompiledPolicy {
  rules: Rule[];
  /** 匹配顺序：优先级降序，同优先级保持声明顺序（稳定排序）。 */
  order: number[];
}

export function ipv4ToInt(text: string): number {
  const parts = text.split(".");
  if (parts.length !== 4) throw new Error(`非法 IPv4 地址: ${text}`);
  let value = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) throw new Error(`非法 IPv4 地址: ${text}`);
    const octet = Number(part);
    if (octet > 255) throw new Error(`非法 IPv4 地址: ${text}`);
    value = value * 256 + octet;
  }
  return value >>> 0;
}

export function intToIpv4(value: number): string {
  return [
    (value >>> 24) & 0xff,
    (value >>> 16) & 0xff,
    (value >>> 8) & 0xff,
    value & 0xff,
  ].join(".");
}

export function prefixMask(length: number): number {
  if (length <= 0) return 0;
  if (length >= 32) return MAX_ADDR;
  return (MAX_ADDR << (32 - length)) >>> 0;
}

function fail(message: string): never {
  throw new Error(message);
}

function checkInt(value: unknown, name: string, min: number, max: number): void {
  if (typeof value !== "number" || !Number.isInteger(value) || value < min || value > max) {
    fail(`${name} 必须是 ${min}..${max} 的整数，得到 ${String(value)}`);
  }
}

function validateCondition(cond: Condition, path: string): void {
  if (typeof cond !== "object" || cond === null) fail(`${path}: 条件必须是对象`);
  switch (cond.kind) {
    case "prefix":
      checkInt(cond.prefix, `${path}.prefix`, 0, MAX_ADDR);
      checkInt(cond.length, `${path}.length`, 0, MAX_PREFIX_LEN);
      if (cond.side !== "src" && cond.side !== "dst") fail(`${path}.side 必须是 src 或 dst`);
      break;
    case "port":
      checkInt(cond.min, `${path}.min`, 0, MAX_PORT);
      checkInt(cond.max, `${path}.max`, 0, MAX_PORT);
      if (cond.min > cond.max) fail(`${path}: 端口区间下界大于上界`);
      if (cond.side !== "src" && cond.side !== "dst") fail(`${path}.side 必须是 src 或 dst`);
      break;
    case "direction":
      if (cond.direction !== "forward" && cond.direction !== "reverse") {
        fail(`${path}.direction 必须是 forward 或 reverse`);
      }
      break;
    case "label":
      checkInt(cond.mask, `${path}.mask`, 0, LABEL_COUNT - 1);
      checkInt(cond.value, `${path}.value`, 0, LABEL_COUNT - 1);
      break;
    case "and":
      if (!Array.isArray(cond.all) || cond.all.length === 0) fail(`${path}.all 必须是非空数组`);
      cond.all.forEach((c, i) => validateCondition(c, `${path}.all[${i}]`));
      break;
    case "or":
      if (!Array.isArray(cond.any) || cond.any.length === 0) fail(`${path}.any 必须是非空数组`);
      cond.any.forEach((c, i) => validateCondition(c, `${path}.any[${i}]`));
      break;
    case "not":
      validateCondition(cond.cond, `${path}.cond`);
      break;
    default:
      fail(`${path}: 未知条件类型 ${String((cond as { kind: unknown }).kind)}`);
  }
}

export function validatePolicy(policy: Policy, name = "policy"): void {
  if (typeof policy !== "object" || policy === null || !Array.isArray(policy.rules)) {
    fail(`${name}: 策略必须包含 rules 数组`);
  }
  if (policy.rules.length > MAX_RULES) {
    fail(`${name}: 规则数 ${policy.rules.length} 超过上限 ${MAX_RULES}`);
  }
  policy.rules.forEach((rule, i) => {
    const path = `${name}.rules[${i}]`;
    checkInt(rule.priority, `${path}.priority`, -Number.MAX_SAFE_INTEGER, Number.MAX_SAFE_INTEGER);
    validateCondition(rule.when, `${path}.when`);
    if (typeof rule.action !== "object" || rule.action === null) fail(`${path}.action 缺失`);
    if (typeof rule.action.allow !== "boolean") fail(`${path}.action.allow 必须是布尔值`);
    if (rule.action.setLabel !== undefined) {
      checkInt(rule.action.setLabel, `${path}.action.setLabel`, 0, LABEL_COUNT - 1);
      if (!rule.action.allow) fail(`${path}: 拒绝动作不得设置标签（拒绝不改变标签）`);
    }
  });
}

export function compilePolicy(policy: Policy, name = "policy"): CompiledPolicy {
  validatePolicy(policy, name);
  const order = policy.rules.map((_, i) => i);
  order.sort((a, b) => policy.rules[b].priority - policy.rules[a].priority);
  return { rules: policy.rules, order };
}

export function matchCondition(cond: Condition, pkt: Packet, label: number): boolean {
  switch (cond.kind) {
    case "prefix": {
      const addr = cond.side === "src" ? pkt.src : pkt.dst;
      const mask = prefixMask(cond.length);
      return ((addr & mask) >>> 0) === ((cond.prefix & mask) >>> 0);
    }
    case "port": {
      const port = cond.side === "src" ? pkt.srcPort : pkt.dstPort;
      return port >= cond.min && port <= cond.max;
    }
    case "direction":
      return pkt.direction === cond.direction;
    case "label":
      return (label & cond.mask) === cond.value;
    case "and":
      return cond.all.every((c) => matchCondition(c, pkt, label));
    case "or":
      return cond.any.some((c) => matchCondition(c, pkt, label));
    case "not":
      return !matchCondition(cond.cond, pkt, label);
  }
}

/** 按策略处理一个报文：返回命中规则、是否允许及处理后标签。 */
export function evalPolicy(compiled: CompiledPolicy, pkt: Packet, label: number): EvalResult {
  for (const index of compiled.order) {
    const rule = compiled.rules[index];
    if (matchCondition(rule.when, pkt, label)) {
      if (!rule.action.allow) return { rule: index, allow: false, label };
      const next = rule.action.setLabel === undefined ? label : rule.action.setLabel;
      return { rule: index, allow: true, label: next };
    }
  }
  return { rule: -1, allow: false, label };
}
