import type { NodeId } from './types.ts';

export interface DampeningConfig {
  /** 每次抖动（Up/Down 翻转）累加的惩罚值 */
  penaltyPerFlap: number;
  /** 超过该阈值则抑制该链路的 Up 通告 */
  suppressThreshold: number;
  /** 惩罚衰减到该阈值以下则解除抑制 */
  reuseThreshold: number;
  /** 惩罚值半衰期（单位：tick） */
  halfLifeTicks: number;
}

export const DEFAULT_DAMPENING: DampeningConfig = {
  penaltyPerFlap: 1000,
  suppressThreshold: 2000,
  reuseThreshold: 750,
  halfLifeTicks: 2,
};

/**
 * 路由震荡抑制（route flap dampening，类似 RFC 2439）：
 * 链路每次状态翻转累加惩罚值，惩罚随 tick 指数衰减；
 * 惩罚超过抑制阈值时，本路由器将该链路按 Down 通告，
 * 直到惩罚衰减到解除阈值以下才恢复，避免全网反复 SPF 震荡。
 */
export class LinkDampener {
  private penalty = new Map<NodeId, number>();
  private suppressed = new Set<NodeId>();
  private readonly owner: NodeId;
  private readonly cfg: DampeningConfig;
  private readonly emit: (msg: string) => void;

  constructor(owner: NodeId, cfg: DampeningConfig, emit: (msg: string) => void) {
    this.owner = owner;
    this.cfg = cfg;
    this.emit = emit;
  }

  /** 链路状态翻转事件 */
  onFlap(neighbor: NodeId, nowUp: boolean): void {
    const p = (this.penalty.get(neighbor) ?? 0) + this.cfg.penaltyPerFlap;
    this.penalty.set(neighbor, p);
    if (nowUp && p >= this.cfg.suppressThreshold && !this.suppressed.has(neighbor)) {
      this.suppressed.add(neighbor);
      this.emit(
        `震荡抑制: 链路 ${this.owner}-${neighbor} 惩罚值 ${Math.round(p)} >= 阈值 ` +
          `${this.cfg.suppressThreshold}, 抑制其 Up 通告(按 Down 处理)`,
      );
    }
  }

  /** 每个 tick 调用一次：惩罚指数衰减，返回本 tick 解除抑制的邻居 */
  decay(): NodeId[] {
    const factor = Math.pow(0.5, 1 / this.cfg.halfLifeTicks);
    const reused: NodeId[] = [];
    for (const [nbr, p] of this.penalty) {
      const np = p * factor;
      this.penalty.set(nbr, np);
      if (this.suppressed.has(nbr) && np < this.cfg.reuseThreshold) {
        this.suppressed.delete(nbr);
        reused.push(nbr);
        this.emit(
          `解除抑制: 链路 ${this.owner}-${nbr} 惩罚值衰减至 ${Math.round(np)} < ` +
            `${this.cfg.reuseThreshold}, 恢复正常通告`,
        );
      }
    }
    return reused;
  }

  /** 该邻居链路当前是否可按 Up 通告 */
  isUsable(neighbor: NodeId): boolean {
    return !this.suppressed.has(neighbor);
  }

  isSuppressed(neighbor: NodeId): boolean {
    return this.suppressed.has(neighbor);
  }
}
