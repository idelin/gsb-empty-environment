/**
 * 最小反例搜索。
 *
 * 模型：3 个图层（自底向上 L1, L2, L3），2 个组，两种切分：
 *   "2+1": 组 {L1,L2} 先合成并量化，再与 L3 合成；
 *   "1+2": 组 {L2,L3} 先合成并量化，再与 L1 合成。
 * 颜色取灰度（r=g=b），中间量化格式为 straight。
 *
 * 2 层不可能产生差异：straight 8 位输入经 解析->量化 是恒等映射，
 * 单层组等价于不分组。因此 3 层 2 组是最小规模。
 *
 * 本文件用纯整数精确运算（与 src/color.ts 的 Frac 语义一一对应）做快速扫描，
 * 找到候选后再用精确有理数库复核（见 main.ts）。
 *
 * 记 a' = 255 - a。各量推导（分母为 255 的幂，分子为整数）：
 *   A12   = (255^2 - a1'a2') / 255^2                    组 alpha
 *   M12   = (r2*a2*255 + r1*a1*a2') / 255^3             组预乘通道
 *   r12   = round(M12/A12 * 255) = round(M12n / A12n)   组 straight 通道
 * 一次性（split 2+1）：
 *   A     = (255^3 - a1'a2'a3') / 255^3
 *   M     = (r3*a3*255^2 + a3'*M12n) / 255^4
 *   out   = round(M/A*255) = round(Mos / Aos)
 * 分组（split 2+1）：组量化重解析后 m12q = r12*a12q（a12q=0 时整组归透明）
 *   A'    = (a3*255 + a12q*a3') / 255^2
 *   M'    = (r3*a3*255 + a3'*m12q) / 255^3
 *   out'  = round(Mgr / Agr)
 * split 1+2 对称（组在上方，over 方向相反，公式见代码）。
 */

export interface Tuple6 {
  a1: number;
  a2: number;
  a3: number;
  r1: number;
  r2: number;
  r3: number;
}

export type Split = "2+1" | "1+2";

export interface FastResult {
  ro: number; // one-shot 灰度通道
  ao: number; // one-shot alpha
  rg: number; // grouped 灰度通道
  ag: number; // grouped alpha
}

/** round-half-up(p/q)，p,q >= 0，q > 0。值域保证不溢出 double 精确范围。 */
function roundDiv(p: number, q: number): number {
  return Math.floor((2 * p + q) / (2 * q));
}

export function evalFast(t: Tuple6, split: Split): FastResult {
  const { a1, a2, a3, r1, r2, r3 } = t;
  if (split === "2+1") {
    const p12 = (255 - a1) * (255 - a2);
    const a12n = 255 * 255 - p12;
    const a12q = roundDiv(a12n, 255);
    const m12n = r2 * a2 * 255 + r1 * a1 * (255 - a2);
    const r12 = a12n === 0 ? 0 : roundDiv(m12n, a12n);
    const m12q = a12q === 0 ? 0 : r12 * a12q; // 零透明度特判：a12q=0 整组透明
    const aos = 255 ** 3 - p12 * (255 - a3);
    const agr = a3 * 255 + a12q * (255 - a3);
    let ao = roundDiv(aos, 255 * 255);
    let ag = roundDiv(agr, 255);
    let ro = aos === 0 ? 0 : roundDiv(r3 * a3 * 255 * 255 + (255 - a3) * m12n, aos);
    let rg = agr === 0 ? 0 : roundDiv(r3 * a3 * 255 + (255 - a3) * m12q, agr);
    if (ao === 0) ro = 0; // 量化为 0 的 alpha：颜色通道不可表示，输出 0
    if (ag === 0) rg = 0;
    return { ro, ao, rg, ag };
  } else {
    const p23 = (255 - a2) * (255 - a3);
    const a23n = 255 * 255 - p23;
    const a23q = roundDiv(a23n, 255);
    const m23n = r3 * a3 * 255 + r2 * a2 * (255 - a3);
    const r23 = a23n === 0 ? 0 : roundDiv(m23n, a23n);
    const m23q = a23q === 0 ? 0 : r23 * a23q;
    const aos = 255 ** 3 - (255 - a1) * p23;
    const agr = a23q * 255 + a1 * (255 - a23q);
    let ao = roundDiv(aos, 255 * 255);
    let ag = roundDiv(agr, 255);
    let ro = aos === 0 ? 0 : roundDiv(m23n * 255 + r1 * a1 * p23, aos);
    let rg = agr === 0 ? 0 : roundDiv(m23q * 255 + r1 * a1 * (255 - a23q), agr);
    if (ao === 0) ro = 0;
    if (ag === 0) rg = 0;
    return { ro, ao, rg, ag };
  }
}

export interface Hit {
  t: Tuple6;
  split: Split;
  result: FastResult;
}

function differs(r: FastResult): boolean {
  return r.ro !== r.rg || r.ao !== r.ag;
}

/**
 * 阶段 A：仅 alpha（颜色通道全 0）穷举，(a1,a2,a3) 字典序，两种切分取更小者。
 */
export function searchAlphaOnly(): Hit | null {
  let best: Hit | null = null;
  for (const split of ["2+1", "1+2"] as const) {
    for (let a1 = 0; a1 <= 255; a1++) {
      for (let a2 = 0; a2 <= 255; a2++) {
        for (let a3 = 0; a3 <= 255; a3++) {
          const t = { a1, a2, a3, r1: 0, r2: 0, r3: 0 };
          const result = evalFast(t, split);
          if (differs(result)) {
            const hit = { t, split, result };
            if (
              best === null ||
              a1 < best.t.a1 ||
              (a1 === best.t.a1 && a2 < best.t.a2) ||
              (a1 === best.t.a1 && a2 === best.t.a2 && a3 < best.t.a3)
            ) {
              best = hit;
            }
            // 同一 split 内字典序第一个即该 split 最小，跳出
            a1 = 256;
            a2 = 256;
            break;
          }
        }
      }
    }
  }
  return best;
}

/**
 * 阶段 B：有界灰度穷举。在 [0..bound]^6 内按字典序找第一个反例（两种切分都试）。
 * 配合递增的 bound 使用，可在“最大分量最小”意义下证明最小性。
 */
export function searchBounded(bound: number): Hit | null {
  for (let a1 = 0; a1 <= bound; a1++)
    for (let a2 = 0; a2 <= bound; a2++)
      for (let a3 = 0; a3 <= bound; a3++)
        for (let r1 = 0; r1 <= bound; r1++)
          for (let r2 = 0; r2 <= bound; r2++)
            for (let r3 = 0; r3 <= bound; r3++) {
              const t = { a1, a2, a3, r1, r2, r3 };
              for (const split of ["2+1", "1+2"] as const) {
                const result = evalFast(t, split);
                if (differs(result)) return { t, split, result };
              }
            }
  return null;
}
