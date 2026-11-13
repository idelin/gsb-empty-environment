import { Rat } from "./rational.ts";
import type { ConstantProductPool, SwapDirection } from "./amm.ts";
import type { LendingHost } from "./lending.ts";

/** 一次模拟所需的全新系统（工厂重建，避免共享未落观察的临时状态）。 */
export interface MarginSystem {
  pool: ConstantProductPool;
  host: LendingHost;
}

export type AttackAction =
  | { kind: "swap"; dir: SwapDirection; amountIn: bigint }
  | { kind: "borrow"; amount: bigint | "max" };

export interface AttackWitness {
  actions: AttackAction[];
  borrowed: bigint;
  badDebt: Rat; // > 0 表示攻击造成坏账
  reserveDrift: { dx: bigint; dy: bigint };
}

export interface AttackSearchOptions {
  makeSystem: () => MarginSystem;
  now: bigint;
  collateral: bigint; // 攻击者抵押的 token0
  reserveTolerance: bigint; // 储备差绝对值上限
  maxActions?: number; // 至多 3 笔
  swapGrid?: bigint[]; // 首笔借入交换的候选输入（token1）
}

export interface SimResult {
  ok: boolean;
  borrowed: bigint;
  badDebt: Rat;
  dx: bigint;
  dy: bigint;
}

const abs = (v: bigint): bigint => (v < 0n ? -v : v);

const FAIL: SimResult = {
  ok: false,
  borrowed: 0n,
  badDebt: Rat.zero(),
  dx: 0n,
  dy: 0n,
};

/** 在全新系统上重放动作序列，评估储备漂移与坏账。 */
export function runAttackScenario(
  opts: AttackSearchOptions,
  actions: AttackAction[],
): SimResult {
  const sys = opts.makeSystem();
  const { pool, host } = sys;
  const x0 = pool.x;
  const y0 = pool.y;
  let borrowed = 0n;
  try {
    for (const action of actions) {
      if (action.kind === "swap") {
        if (action.amountIn <= 0n) return FAIL;
        pool.swap(action.amountIn, action.dir, opts.now);
      } else {
        const receipt = host.beginTx(opts.now); // 同一交易先固定回执
        if (host.collateral === 0n) host.deposit(opts.collateral);
        const amount =
          action.amount === "max" ? host.maxBorrow(receipt) : action.amount;
        if (amount <= 0n) return FAIL;
        host.borrow(receipt, amount);
        borrowed += amount;
      }
    }
  } catch {
    return FAIL;
  }
  const dx = pool.x - x0;
  const dy = pool.y - y0;
  if (abs(dx) > opts.reserveTolerance || abs(dy) > opts.reserveTolerance) {
    return FAIL;
  }
  if (borrowed <= 0n) return FAIL;
  const truePrice = Rat.of(pool.y, pool.x);
  const badDebt = Rat.of(borrowed).sub(truePrice.scale(opts.collateral));
  return { ok: true, borrowed, badDebt, dx, dy };
}

/**
 * 反向交换求解：找最小 b，使 zeroForOne 交换后 y 回到 [targetY-tol, targetY+tol]。
 * 输出关于输入单调且步长不超过 1，二分可解。
 */
function solveReverseSwap(
  pool: ConstantProductPool,
  targetY: bigint,
  tol: bigint,
): bigint | null {
  const need = pool.y - targetY;
  if (need <= 0n) return null;
  const lo = need - tol < 0n ? 0n : need - tol;
  const hi = need + tol;
  if (hi > pool.y - 1n) return null;
  const out = (b: bigint): bigint => {
    try {
      return pool.getAmountOut(b, "zeroForOne");
    } catch {
      return 0n;
    }
  };
  let loB = 1n;
  let hiB = 1n;
  while (out(hiB) < lo) {
    hiB *= 2n;
    if (hiB > 1n << 200n) return null;
  }
  while (loB < hiB) {
    const mid = (loB + hiB) / 2n;
    if (out(mid) >= lo) hiB = mid;
    else loB = mid + 1n;
  }
  const o = out(loB);
  return o >= lo && o <= hi ? loB : null;
}

/**
 * 至多 3 笔攻击动作搜索：按动作数 BFS，返回
 * “借入交换 → 借款 → 反向交换” 后储备在容差内恢复且坏账为正的最短见证。
 */
export function findAttackWitness(
  opts: AttackSearchOptions,
): AttackWitness | null {
  const maxActions = Math.min(opts.maxActions ?? 3, 3);
  const probe = opts.makeSystem();
  const y0 = probe.pool.y;
  const grid =
    opts.swapGrid ??
    [y0 / 64n, y0 / 16n, y0 / 4n, y0 / 2n, y0].filter((v) => v > 0n);
  const tol = opts.reserveTolerance;

  const tryActions = (actions: AttackAction[]): AttackWitness | null => {
    const r = runAttackScenario(opts, actions);
    if (!r.ok || !r.badDebt.isPositive()) return null;
    return {
      actions: actions.map((a) =>
        a.kind === "borrow" && a.amount === "max"
          ? { kind: "borrow", amount: r.borrowed }
          : a,
      ),
      borrowed: r.borrowed,
      badDebt: r.badDebt,
      reserveDrift: { dx: r.dx, dy: r.dy },
    };
  };

  if (maxActions >= 1) {
    const w = tryActions([{ kind: "borrow", amount: "max" }]);
    if (w) return w;
  }

  if (maxActions >= 2) {
    for (const a of grid) {
      const w = tryActions([
        { kind: "swap", dir: "oneForZero", amountIn: a },
        { kind: "borrow", amount: "max" },
      ]);
      if (w) return w;
    }
  }

  if (maxActions >= 3) {
    for (const a of grid) {
      const scratch = opts.makeSystem();
      try {
        scratch.pool.swap(a, "oneForZero", opts.now);
      } catch {
        continue;
      }
      const b = solveReverseSwap(scratch.pool, y0, tol);
      if (b === null) continue;
      const w = tryActions([
        { kind: "swap", dir: "oneForZero", amountIn: a },
        { kind: "borrow", amount: "max" },
        { kind: "swap", dir: "zeroForOne", amountIn: b },
      ]);
      if (w) return w;
    }
  }

  return null;
}
