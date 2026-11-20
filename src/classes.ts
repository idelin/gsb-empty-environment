/**
 * 抽象类别计算。
 *
 * 把两份策略中出现的所有地址前缀边界与端口区间边界取并集，将
 * (源地址, 目的地址, 源端口, 目的端口) 的空间切成有限个等价类：
 * 同一类别中的任意两个地址/端口组合，对两份策略的所有前缀/端口条件
 * 求值结果完全一致。因此每类只需一个代表点即可穷举全部行为，
 * 无需枚举单个地址（更不需要枚举 IPv6 地址）。
 */

import { MAX_ADDR, MAX_PORT, prefixMask } from "./model.ts";
import type { Condition, Direction, Packet, Policy } from "./model.ts";

export interface ClassInfo {
  id: number;
  srcLo: number;
  srcHi: number;
  dstLo: number;
  dstHi: number;
  srcPortLo: number;
  srcPortHi: number;
  dstPortLo: number;
  dstPortHi: number;
  /** 类别代表点：取各区间下界（区间内的点对所有原子条件等价）。 */
  rep: { src: number; dst: number; srcPort: number; dstPort: number };
}

function eachCondition(policy: Policy, fn: (cond: Condition) => void): void {
  const walk = (cond: Condition): void => {
    fn(cond);
    if (cond.kind === "and") cond.all.forEach(walk);
    else if (cond.kind === "or") cond.any.forEach(walk);
    else if (cond.kind === "not") walk(cond.cond);
  };
  for (const rule of policy.rules) walk(rule.when);
}

function addrBoundaries(policies: Policy[]): number[] {
  const cuts = new Set<number>([0]);
  for (const policy of policies) {
    eachCondition(policy, (cond) => {
      if (cond.kind !== "prefix") return;
      const mask = prefixMask(cond.length);
      const base = (cond.prefix & mask) >>> 0;
      const end = base + 2 ** (32 - cond.length) - 1;
      cuts.add(base);
      if (end < MAX_ADDR) cuts.add(end + 1);
    });
  }
  return [...cuts].sort((a, b) => a - b);
}

function portBoundaries(policies: Policy[]): number[] {
  const cuts = new Set<number>([0]);
  for (const policy of policies) {
    eachCondition(policy, (cond) => {
      if (cond.kind !== "port") return;
      cuts.add(cond.min);
      if (cond.max < MAX_PORT) cuts.add(cond.max + 1);
    });
  }
  return [...cuts].sort((a, b) => a - b);
}

interface Interval {
  lo: number;
  hi: number;
}

function toIntervals(cuts: number[], top: number): Interval[] {
  const out: Interval[] = [];
  for (let i = 0; i < cuts.length; i++) {
    out.push({ lo: cuts[i], hi: i + 1 < cuts.length ? cuts[i + 1] - 1 : top });
  }
  return out;
}

/** 计算两份策略联合诱导的全部抽象类别（全枚举，非抽样）。 */
export function computeClasses(oldPolicy: Policy, newPolicy: Policy): ClassInfo[] {
  const policies = [oldPolicy, newPolicy];
  const addrIntervals = toIntervals(addrBoundaries(policies), MAX_ADDR);
  const portIntervals = toIntervals(portBoundaries(policies), MAX_PORT);
  const classes: ClassInfo[] = [];
  let id = 0;
  for (const src of addrIntervals) {
    for (const dst of addrIntervals) {
      for (const srcPort of portIntervals) {
        for (const dstPort of portIntervals) {
          classes.push({
            id: id++,
            srcLo: src.lo,
            srcHi: src.hi,
            dstLo: dst.lo,
            dstHi: dst.hi,
            srcPortLo: srcPort.lo,
            srcPortHi: srcPort.hi,
            dstPortLo: dstPort.lo,
            dstPortHi: dstPort.hi,
            rep: { src: src.lo, dst: dst.lo, srcPort: srcPort.lo, dstPort: dstPort.lo },
          });
        }
      }
    }
  }
  return classes;
}

/** 类别代表点上某个方向的报文；反向报文交换五元组的两端。 */
export function packetFor(cls: ClassInfo, direction: Direction): Packet {
  if (direction === "forward") {
    return {
      src: cls.rep.src,
      dst: cls.rep.dst,
      srcPort: cls.rep.srcPort,
      dstPort: cls.rep.dstPort,
      direction,
    };
  }
  return {
    src: cls.rep.dst,
    dst: cls.rep.src,
    srcPort: cls.rep.dstPort,
    dstPort: cls.rep.srcPort,
    direction,
  };
}
