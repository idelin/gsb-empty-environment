/**
 * 入口：演示库功能 -> 自检 -> 搜索最小反例 -> 用精确有理数复核并输出报告。
 * 运行：npm start   （Node >= 23.6，原生类型擦除运行 .ts）
 */

import { Frac } from "./fraction.ts";
import {
  composeOver,
  fromPremultiplied8,
  fromStraight8,
  toStraight8,
  type ExactColor,
  type RGBA8,
} from "./color.ts";
import {
  compositeGrouped,
  compositeOneShot,
  diffChannels,
  parseLayer,
  type Layer,
} from "./compare.ts";
import {
  evalFast,
  searchAlphaOnly,
  searchBounded,
  type Hit,
} from "./search.ts";

function fmt(c: RGBA8): string {
  return `(r=${c.r}, g=${c.g}, b=${c.b}, a=${c.a})`;
}

function fmtExact(c: ExactColor): string {
  return `r=${c.r} g=${c.g} b=${c.b} a=${c.a}`;
}

function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`self-test failed: ${msg}`);
}

// ---------- 1. 自检 ----------
function selfTest(): void {
  // straight 8 位 -> 精确 -> 量化 是恒等映射（a=0 时归全透明）
  const samples = [0, 1, 2, 127, 128, 200, 254, 255];
  for (const a of samples) {
    for (const r of samples) {
      const c = { r, g: 255 - r, b: r, a };
      const back = toStraight8(fromStraight8(c));
      const expect = a === 0 ? { r: 0, g: 0, b: 0, a: 0 } : c;
      assert(JSON.stringify(back) === JSON.stringify(expect), `round-trip ${JSON.stringify(c)}`);
    }
  }
  // 预乘输入解析
  const p = fromPremultiplied8({ r: 128, g: 0, b: 64, a: 200 });
  assert(p.r.toString() === "128/255" && p.a.toString() === "40/51", "premultiplied parse");
  // 2 层任意分组不产生差异（单层组 = 恒等）
  const l1: Layer = { format: "straight", color: { r: 10, g: 20, b: 30, a: 100 } };
  const l2: Layer = { format: "straight", color: { r: 200, g: 100, b: 50, a: 77 } };
  const one = compositeOneShot([l1, l2]);
  const grp = compositeGrouped([l1, l2], [1, 1]);
  assert(JSON.stringify(one) === JSON.stringify(grp), "2-layer grouping must be identity");
  console.log("[self-test] all passed");
}

// ---------- 2. 功能演示 ----------
function demo(): void {
  console.log("\n== 格式与零透明度演示 ==");
  const s = fromStraight8({ r: 200, g: 30, b: 0, a: 128 });
  console.log(`straight (200,30,0,128)   -> 精确预乘 ${fmtExact(s)}`);
  const p = fromPremultiplied8({ r: 100, g: 12, b: 0, a: 128 });
  console.log(`premultiplied (100,12,0,128) -> 精确预乘 ${fmtExact(p)}`);
  const z = fromStraight8({ r: 200, g: 30, b: 0, a: 0 });
  console.log(`straight (200,30,0,0) a=0 特判 -> ${fmtExact(z)}（颜色通道丢弃，归全透明）`);
  const back = toStraight8(composeOver([z]));
  console.log(`全透明合成后量化           -> ${fmt(back)}（反预乘零 alpha 特判，通道输出 0）`);
}

// ---------- 3. 搜索 ----------
function findCounterexample(): { hit: Hit; provenBound: number } {
  console.log("\n== 搜索最小反例 ==");
  console.log("模型：3 层（2 层可证无差异）+ 2 组，切分 2+1 / 1+2，灰度通道，中间量化 straight");
  const t0 = Date.now();
  const alphaHit = searchAlphaOnly();
  console.log(`阶段 A（仅 alpha 穷举，256^3）耗时 ${Date.now() - t0}ms`);
  if (alphaHit) {
    console.log(
      `  仅 alpha 反例：a=(${alphaHit.t.a1},${alphaHit.t.a2},${alphaHit.t.a3}) split=${alphaHit.split}`,
    );
  } else {
    console.log("  仅 alpha 无反例");
  }

  // 阶段 B：按“最大分量”递增做有界穷举，证明该意义下的最小性
  const alphaMax = alphaHit
    ? Math.max(alphaHit.t.a1, alphaHit.t.a2, alphaHit.t.a3)
    : 256;
  const cap = Math.min(alphaMax, 32); // 32^6 约 1e9 次求值，数秒级
  let hit: Hit | null = null;
  let bound = 0;
  for (bound = 0; bound <= cap; bound++) {
    const t1 = Date.now();
    hit = searchBounded(bound);
    if (hit) {
      console.log(`阶段 B：在最大分量 <= ${bound} 处找到反例（耗时 ${Date.now() - t1}ms）`);
      break;
    }
    if (Date.now() - t0 > 60_000) {
      console.log(`阶段 B：时间预算用完，已证最大分量 <= ${bound} 内无反例`);
      break;
    }
  }
  if (hit) return { hit, provenBound: bound };
  if (alphaHit) return { hit: alphaHit, provenBound: cap };
  throw new Error("no counterexample found");
}

// ---------- 4. 精确复核与报告 ----------
function report(hit: Hit, provenBound: number): void {
  const { t, split } = hit;
  const layers: Layer[] = [
    { format: "straight", color: { r: t.r1, g: t.r1, b: t.r1, a: t.a1 } },
    { format: "straight", color: { r: t.r2, g: t.r2, b: t.r2, a: t.a2 } },
    { format: "straight", color: { r: t.r3, g: t.r3, b: t.r3, a: t.a3 } },
  ];
  const groupSizes = split === "2+1" ? [2, 1] : [1, 2];

  const oneShot = compositeOneShot(layers);
  const grouped = compositeGrouped(layers, groupSizes);

  // 快速整数扫描结果必须与精确有理数库一致
  const fast = evalFast(t, split);
  assert(fast.ro === oneShot.r && fast.ao === oneShot.a, "fast one-shot mismatch");
  assert(fast.rg === grouped.r && fast.ag === grouped.a, "fast grouped mismatch");

  console.log("\n== 最小反例（精确有理数复核） ==");
  console.log(`图层（自底向上，straight 8 位，r=g=b）：`);
  layers.forEach((l, i) => console.log(`  L${i + 1} = ${fmt(l.color)}`));
  console.log(`分组：${split === "2+1" ? "{L1,L2} + {L3}" : "{L1} + {L2,L3}"}`);

  // 组内精确中间值
  const gi = split === "2+1" ? [0, 1] : [1, 2];
  const groupExact = composeOver([parseLayer(layers[gi[0]]), parseLayer(layers[gi[1]])]);
  const groupQuant = toStraight8(groupExact);
  console.log(`\n组 {L${gi[0] + 1},L${gi[1] + 1}} 精确合成：${fmtExact(groupExact)}`);
  console.log(`  其中 alpha = ${groupExact.a} ≈ ${groupExact.a.toNumber().toFixed(6)}，通道 r = ${groupExact.r} ≈ ${groupExact.r.toNumber().toFixed(6)}`);
  console.log(`  量化为 8 位 straight：${fmt(groupQuant)}  <- 信息在此丢失`);

  const oneShotExact = composeOver(layers.map(parseLayer));
  console.log(`\n一次性精确合成：${fmtExact(oneShotExact)}`);
  console.log(`  量化结果：${fmt(oneShot)}`);
  console.log(`分组量化合成：${fmt(grouped)}`);
  const d = diffChannels(oneShot, grouped);
  console.log(`\n差异通道：${d.join("; ")}`);
  console.log(
    `最小性：层数 3（2 层可证无差异）、组数 2；已穷举证明所有分量 <= ${provenBound - 1} 的输入无反例，` +
      `本例最大分量为 ${Math.max(t.a1, t.a2, t.a3, t.r1, t.r2, t.r3)}。`,
  );
}

selfTest();
demo();
const { hit, provenBound } = findCounterexample();
report(hit, provenBound);
