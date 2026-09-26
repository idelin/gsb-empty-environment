import type { BoundaryPolicy, Halo, SeparableKernel } from './types.ts';

export interface KernelSpec {
  id: string;
  hx: ArrayLike<number>;
  hy: ArrayLike<number>;
  anchorX: number;
  anchorY: number;
  boundaryX?: BoundaryPolicy;
  boundaryY?: BoundaryPolicy;
}

const POLICIES: readonly BoundaryPolicy[] = ['clamp', 'mirror', 'wrap', 'zero'];

export function makeKernel(spec: KernelSpec): SeparableKernel {
  const hx = Float64Array.from(spec.hx);
  const hy = Float64Array.from(spec.hy);
  if (hx.length < 1 || hy.length < 1) {
    throw new Error(`kernel "${spec.id}": 核长度必须 >= 1`);
  }
  checkAnchor(spec.id, spec.anchorX, hx.length, 'anchorX');
  checkAnchor(spec.id, spec.anchorY, hy.length, 'anchorY');
  const boundaryX = spec.boundaryX ?? 'clamp';
  const boundaryY = spec.boundaryY ?? 'clamp';
  if (!POLICIES.includes(boundaryX) || !POLICIES.includes(boundaryY)) {
    throw new Error(`kernel "${spec.id}": 未知边界策略`);
  }
  return Object.freeze({
    id: spec.id,
    hx,
    hy,
    anchorX: spec.anchorX,
    anchorY: spec.anchorY,
    boundaryX,
    boundaryY,
  });
}

function checkAnchor(id: string, anchor: number, length: number, name: string): void {
  if (!Number.isInteger(anchor) || anchor < 0 || anchor >= length) {
    throw new RangeError(`kernel "${id}": ${name}=${anchor} 越界, 合法范围 [0, ${length})`);
  }
}

// 单级核的 halo: out(x) = sum_j k[j] * in(x + j - anchor)
export function stageHalo(k: SeparableKernel): Halo {
  return {
    left: k.anchorX,
    right: k.hx.length - 1 - k.anchorX,
    top: k.anchorY,
    bottom: k.hy.length - 1 - k.anchorY,
  };
}

// 融合执行时串联链的总 halo = 各级 halo 之和
export function chainHalo(kernels: readonly SeparableKernel[]): Halo {
  const total: Halo = { left: 0, right: 0, top: 0, bottom: 0 };
  for (const k of kernels) {
    const h = stageHalo(k);
    total.left += h.left;
    total.right += h.right;
    total.top += h.top;
    total.bottom += h.bottom;
  }
  return total;
}
