import { linearToSrgb, srgbToLinear } from '../src/color.ts';
import type { Layer } from '../src/layer.ts';

export function solidLayer(
  id: string,
  w: number,
  h: number,
  rgba: [number, number, number, number],
  opts: Partial<Layer> = {},
): Layer {
  const pixels = new Float32Array(w * h * 4);
  for (let i = 0; i < w * h; i++) pixels.set(rgba, i * 4);
  return {
    id,
    width: w,
    height: h,
    pixels,
    colorSpace: 'srgb',
    premultiplied: false,
    offsetX: 0,
    offsetY: 0,
    crop: null,
    blend: 'source-over',
    opacity: 1,
    inputs: [],
    visible: true,
    ...opts,
  };
}

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// 独立逐像素参考实现：不复用合成器的瓦片/调度代码，直接对整幅画布逐像素合成。
export function referenceRender(layers: Layer[], W: number, H: number): Float32Array {
  const all = new Map(layers.map((l) => [l.id, l]));
  const done = new Set<string>();
  const remaining = layers.filter((l) => l.visible);
  const order: Layer[] = [];
  while (remaining.length > 0) {
    const idx = remaining.findIndex((l) =>
      l.inputs.every((d) => done.has(d) || !all.get(d)?.visible),
    );
    if (idx < 0) throw new Error('reference: cycle');
    const [l] = remaining.splice(idx, 1);
    done.add(l.id);
    order.push(l);
  }
  const working = order.some((l) => l.colorSpace === 'linear') ? 'linear' : 'srgb';
  const acc = new Float64Array(W * H * 4); // 工作空间、预乘
  for (const L of order) {
    const crop = L.crop ?? { x: 0, y: 0, w: L.width, h: L.height };
    for (let y = 0; y < crop.h; y++) {
      for (let x = 0; x < crop.w; x++) {
        const cx = L.offsetX + x;
        const cy = L.offsetY + y;
        if (cx < 0 || cy < 0 || cx >= W || cy >= H) continue;
        const lx = crop.x + x;
        const ly = crop.y + y;
        if (lx < 0 || ly < 0 || lx >= L.width || ly >= L.height) continue;
        const si = (ly * L.width + lx) * 4;
        let r = L.pixels[si];
        let g = L.pixels[si + 1];
        let b = L.pixels[si + 2];
        const a = L.pixels[si + 3];
        if (L.colorSpace !== working) {
          const f = L.colorSpace === 'srgb' ? srgbToLinear : linearToSrgb;
          r = f(r);
          g = f(g);
          b = f(b);
        }
        if (!L.premultiplied) {
          r *= a;
          g *= a;
          b *= a;
        }
        const sa = a * L.opacity;
        const sr = r * L.opacity;
        const sg = g * L.opacity;
        const sb = b * L.opacity;
        const di = (cy * W + cx) * 4;
        if (L.blend === 'source-over') {
          const k = 1 - sa;
          acc[di] = sr + acc[di] * k;
          acc[di + 1] = sg + acc[di + 1] * k;
          acc[di + 2] = sb + acc[di + 2] * k;
          acc[di + 3] = sa + acc[di + 3] * k;
        } else if (L.blend === 'lighter') {
          acc[di] += sr;
          acc[di + 1] += sg;
          acc[di + 2] += sb;
          acc[di + 3] += sa;
        } else if (L.blend === 'multiply') {
          const da = acc[di + 3];
          const dr = da > 0 ? acc[di] / da : 0;
          const dg = da > 0 ? acc[di + 1] / da : 0;
          const db = da > 0 ? acc[di + 2] / da : 0;
          const k = 1 - sa;
          acc[di] = sr * dr + acc[di] * k;
          acc[di + 1] = sg * dg + acc[di + 1] * k;
          acc[di + 2] = sb * db + acc[di + 2] * k;
          acc[di + 3] = sa + da * k;
        }
      }
    }
  }
  const out = new Float32Array(W * H * 4);
  for (let i = 0; i < W * H; i++) {
    let r = acc[i * 4];
    let g = acc[i * 4 + 1];
    let b = acc[i * 4 + 2];
    const a = acc[i * 4 + 3];
    if (a > 0) {
      r /= a;
      g /= a;
      b /= a;
    }
    if (working === 'linear') {
      r = linearToSrgb(r);
      g = linearToSrgb(g);
      b = linearToSrgb(b);
    }
    out[i * 4] = r;
    out[i * 4 + 1] = g;
    out[i * 4 + 2] = b;
    out[i * 4 + 3] = a;
  }
  return out;
}

export function randomLayer(rng: () => number, id: string, W: number, H: number, deps: string[]): Layer {
  const w = 1 + Math.floor(rng() * W);
  const h = 1 + Math.floor(rng() * H);
  const colorSpace = rng() < 0.5 ? 'srgb' : 'linear';
  const premultiplied = rng() < 0.5;
  const pixels = new Float32Array(w * h * 4);
  for (let i = 0; i < w * h; i++) {
    const a = 0.15 + rng() * 0.85;
    let r = rng();
    let g = rng();
    let b = rng();
    if (premultiplied) {
      r *= a;
      g *= a;
      b *= a;
    }
    pixels.set([r, g, b, a], i * 4);
  }
  const blends = ['source-over', 'source-over', 'lighter', 'multiply'] as const;
  let crop = null;
  if (rng() < 0.4 && w > 1 && h > 1) {
    const cx = Math.floor(rng() * (w - 1));
    const cy = Math.floor(rng() * (h - 1));
    crop = { x: cx, y: cy, w: 1 + Math.floor(rng() * (w - cx)), h: 1 + Math.floor(rng() * (h - cy)) };
  }
  return {
    id,
    width: w,
    height: h,
    pixels,
    colorSpace,
    premultiplied,
    offsetX: Math.floor(rng() * (W + 2)) - 1,
    offsetY: Math.floor(rng() * (H + 2)) - 1,
    crop,
    blend: blends[Math.floor(rng() * blends.length)],
    opacity: 0.3 + rng() * 0.7,
    inputs: deps,
    visible: true,
  };
}

export function assertClose(actual: Float32Array, expected: Float32Array, eps: number, what: string): void {
  if (actual.length !== expected.length) throw new Error(`${what}: length mismatch`);
  for (let i = 0; i < actual.length; i++) {
    const d = Math.abs(actual[i] - expected[i]);
    if (d > eps) {
      throw new Error(`${what}: mismatch at component ${i} (pixel ${Math.floor(i / 4)}): got ${actual[i]}, want ${expected[i]}`);
    }
  }
}
