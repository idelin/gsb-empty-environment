import type { ColorSpace } from './color.ts';
import { layerBounds, type Layer, type Rect } from './layer.ts';

export interface PlanStep {
  layer: Layer;
  bounds: Rect;
  convert: boolean;     // 需要把图层像素转换到工作色彩空间
  premultiply: boolean; // 需要预乘（已预乘的图层恒为 false，绝不重复预乘）
}

export interface Plan {
  workingSpace: ColorSpace;
  steps: PlanStep[]; // 自下而上的合成顺序
}

const KNOWN_BLENDS = new Set(['source-over', 'lighter', 'multiply']);

// 把图层集合编译成合成计划：拓扑排序定序、拒绝循环、按需插入颜色转换。
export function compilePlan(layers: readonly Layer[], canvasW: number, canvasH: number): Plan {
  const byId = new Map<string, Layer>();
  for (const l of layers) {
    if (byId.has(l.id)) throw new Error(`duplicate layer id: ${l.id}`);
    byId.set(l.id, l);
  }
  for (const l of layers) {
    if (!KNOWN_BLENDS.has(l.blend)) throw new Error(`unknown blend op: ${l.blend}`);
    for (const dep of l.inputs) {
      if (!byId.has(dep)) throw new Error(`layer ${l.id} depends on unknown layer ${dep}`);
    }
  }

  // 循环检测（DFS 三色标记）
  const state = new Map<string, 1 | 2>();
  const visit = (id: string, stack: string[]): void => {
    const s = state.get(id);
    if (s === 1) throw new Error(`dependency cycle: ${[...stack, id].join(' -> ')}`);
    if (s === 2) return;
    state.set(id, 1);
    for (const dep of byId.get(id)!.inputs) visit(dep, [...stack, id]);
    state.set(id, 2);
  };
  for (const l of layers) visit(l.id, []);

  // 拓扑排序：每轮按插入序取首个依赖均已就绪的图层，保证确定且尽量贴近插入序
  const emitted = new Set<string>();
  const remaining = [...layers];
  const ordered: Layer[] = [];
  while (remaining.length > 0) {
    const idx = remaining.findIndex((l) => l.inputs.every((d) => emitted.has(d)));
    if (idx < 0) throw new Error('dependency cycle'); // 理论上已被上方 DFS 拦截
    const [l] = remaining.splice(idx, 1);
    emitted.add(l.id);
    ordered.push(l);
  }

  const visible = ordered.filter((l) => l.visible);
  // 工作空间：任一图层为线性光则整体在线性空间合成，否则直接 sRGB
  const workingSpace: ColorSpace = visible.some((l) => l.colorSpace === 'linear') ? 'linear' : 'srgb';

  const steps: PlanStep[] = [];
  for (const layer of visible) {
    const bounds = layerBounds(layer, canvasW, canvasH);
    if (!bounds) continue;
    steps.push({
      layer,
      bounds,
      convert: layer.colorSpace !== workingSpace,
      premultiply: !layer.premultiplied,
    });
  }
  return { workingSpace, steps };
}
