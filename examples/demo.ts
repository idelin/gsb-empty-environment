/**
 * 运行样例：测量 -> 热更新 -> 精确失效 -> 晚到加载校验 -> 缓存统计。
 * 运行：node examples/demo.ts （需要 Node >= 22.6，推荐 >= 23.6）
 */
import {
  ByteCache,
  FontRegistry,
  LayoutEngine,
  TextMeasurer,
  monoV1,
  sampleTree,
  serifV1,
  serifV2,
  type MeasureEvent,
  type StyleSpec,
} from '../src/index.ts';

const registry = new FontRegistry([serifV1, monoV1]);
const cache = new ByteCache(200_000, { font: 40_000, cluster: 60_000, measure: 100_000 });
const measurer = new TextMeasurer(registry, cache, { auto: true });

function measureAsync(text: string, style: StyleSpec): Promise<MeasureEvent[]> {
  return new Promise((resolve) => {
    const events: MeasureEvent[] = [];
    measurer.measureText(text, style, (e) => {
      events.push(e);
      if (e.phase === 'final') resolve(events);
    });
  });
}

function fmt(n: number): string {
  return n.toFixed(2);
}

// 1. 布局节点树（核心计算纯数据驱动，不借浏览器布局引擎）
const engine = new LayoutEngine(measurer);
const layout = engine.layout(sampleTree);
console.log('== 布局测量（预估与最终分开发布） ==');
for (const [key, result] of layout.measured) {
  console.log(`  final ${key} -> width=${fmt(result.width)} height=${fmt(result.height)}`);
}

// 2. 相同文本、不同字距：结果独立
const style0: StyleSpec = { fontId: 'serif', fontSize: 16, letterSpacing: 0 };
const style2: StyleSpec = { fontId: 'serif', fontSize: 16, letterSpacing: 2 };
const [e0, e2] = await Promise.all([
  measureAsync('Hello', style0),
  measureAsync('Hello', style2),
]);
console.log('\n== 相同文本不同字距 ==');
console.log(`  letterSpacing=0 estimate=${fmt(e0[0].result.width)} final=${fmt(e0[1].result.width)}`);
console.log(`  letterSpacing=2 estimate=${fmt(e2[0].result.width)} final=${fmt(e2[1].result.width)}`);

// 3. 字体热更新：原子切换代际 + 精确失效
const before = registry.snapshot();
registry.hotReload(serifV2);
console.log('\n== 字体热更新 ==');
console.log(`  代际 ${before.generation} -> ${registry.snapshot().generation}（原子切换）`);
const after = await measureAsync('Hello', style0);
console.log(`  'Hello' 重测 final=${fmt(after[1].result.width)}（旧值 ${fmt(e0[1].result.width)} 已失效）`);

// 4. 晚到的字体加载：旧版本被拒绝
const late = await registry.loadFont(() => Promise.resolve(serifV1));
console.log('\n== 晚到字体加载 ==');
console.log(`  v1 晚到 applied=${late.applied}，当前版本=${registry.snapshot().get('serif')!.version}`);

// 5. 缓存统计：额度守恒
const stats = cache.stats();
console.log('\n== 缓存统计 ==');
for (const [name, p] of Object.entries(stats.partitions)) {
  console.log(`  ${name}: used=${p.used}/${p.quota} entries=${p.count}`);
}
console.log(`  总额度守恒: ${stats.conserved}（${stats.totalQuota}/${stats.totalBudget}）`);
