import { GrayImage } from './image.ts';
import { seRadius } from './se.ts';
import type { StructuringElement } from './se.ts';
import type { BoundaryMode, Rect } from './types.ts';

/** Sample with boundary handling; returns null for 'skip' out-of-bounds. */
function sample(img: GrayImage, x: number, y: number, mode: BoundaryMode): number | null {
  if (x >= 0 && y >= 0 && x < img.width && y < img.height) {
    return img.data[y * img.width + x];
  }
  switch (mode) {
    case 'zero':
      return 0;
    case 'max':
      return 255;
    case 'clamp': {
      const cx = x < 0 ? 0 : x >= img.width ? img.width - 1 : x;
      const cy = y < 0 ? 0 : y >= img.height ? img.height - 1 : y;
      return img.data[cy * img.width + cx];
    }
    case 'skip':
      return null;
  }
}

export function expandRect(rect: Rect, rx: number, ry: number, width: number, height: number): Rect {
  return {
    x0: Math.max(0, rect.x0 - rx),
    y0: Math.max(0, rect.y0 - ry),
    x1: Math.min(width, rect.x1 + rx),
    y1: Math.min(height, rect.y1 + ry),
  };
}

export function fullRect(width: number, height: number): Rect {
  return { x0: 0, y0: 0, x1: width, y1: height };
}

/** Dilation: out(x) = max over offsets (dx,dy) of in(x - dx, y - dy). */
export function dilateRect(
  input: GrayImage,
  se: StructuringElement,
  boundary: BoundaryMode,
  rect: Rect,
  out: GrayImage,
): void {
  for (let y = rect.y0; y < rect.y1; y++) {
    for (let x = rect.x0; x < rect.x1; x++) {
      let v = 0;
      for (const [dx, dy] of se.offsets) {
        const s = sample(input, x - dx, y - dy, boundary);
        if (s !== null && s > v) v = s;
      }
      out.data[y * out.width + x] = v;
    }
  }
}

/** Erosion: out(x) = min over offsets (dx,dy) of in(x + dx, y + dy). */
export function erodeRect(
  input: GrayImage,
  se: StructuringElement,
  boundary: BoundaryMode,
  rect: Rect,
  out: GrayImage,
): void {
  for (let y = rect.y0; y < rect.y1; y++) {
    for (let x = rect.x0; x < rect.x1; x++) {
      let v = 255;
      for (const [dx, dy] of se.offsets) {
        const s = sample(input, x + dx, y + dy, boundary);
        if (s !== null && s < v) v = s;
      }
      out.data[y * out.width + x] = v;
    }
  }
}

/**
 * Opening (erode then dilate) restricted to `rect`. The intermediate erosion
 * is computed on the rect expanded by the SE radius, which is exactly the
 * region the final dilation samples, so the result matches a full-image open.
 */
export function openRect(
  input: GrayImage,
  se: StructuringElement,
  erodeBoundary: BoundaryMode,
  dilateBoundary: BoundaryMode,
  rect: Rect,
  out: GrayImage,
): void {
  const { rx, ry } = seRadius(se);
  const inner = expandRect(rect, rx, ry, input.width, input.height);
  const temp = new GrayImage(input.width, input.height);
  erodeRect(input, se, erodeBoundary, inner, temp);
  dilateRect(temp, se, dilateBoundary, rect, out);
}

/** Closing (dilate then erode) restricted to `rect`; see openRect. */
export function closeRect(
  input: GrayImage,
  se: StructuringElement,
  dilateBoundary: BoundaryMode,
  erodeBoundary: BoundaryMode,
  rect: Rect,
  out: GrayImage,
): void {
  const { rx, ry } = seRadius(se);
  const inner = expandRect(rect, rx, ry, input.width, input.height);
  const temp = new GrayImage(input.width, input.height);
  dilateRect(input, se, dilateBoundary, inner, temp);
  erodeRect(temp, se, erodeBoundary, rect, out);
}

/** Where label is 0, copy the input pixel through (stage-local label mask). */
export function applyLabel(
  out: GrayImage,
  input: GrayImage,
  label: Uint8Array | null | undefined,
  rect: Rect,
): void {
  if (!label) return;
  for (let y = rect.y0; y < rect.y1; y++) {
    for (let x = rect.x0; x < rect.x1; x++) {
      const i = y * out.width + x;
      if (!label[i]) out.data[i] = input.data[i];
    }
  }
}

export interface LocalOpOptions {
  se: StructuringElement;
  /**
   * Boundary mode for out-of-image samples. Defaults are the dual pair that
   * keeps opening anti-extensive and closing extensive: dilation pads with 0,
   * erosion pads with 255. An explicit mode applies to both steps of
   * open/close (note: eroding with 'zero' can shrink border foreground).
   */
  boundary?: BoundaryMode;
  label?: Uint8Array | null;
  rect?: Rect | null;
}

export function applyLocal(
  kind: 'dilate' | 'erode' | 'open' | 'close',
  input: GrayImage,
  opts: LocalOpOptions,
  out?: GrayImage,
): GrayImage {
  const rect = opts.rect ?? fullRect(input.width, input.height);
  const result = out ?? input.clone();
  const dilateBoundary = opts.boundary ?? 'zero';
  const erodeBoundary = opts.boundary ?? 'max';
  switch (kind) {
    case 'dilate':
      dilateRect(input, opts.se, dilateBoundary, rect, result);
      break;
    case 'erode':
      erodeRect(input, opts.se, erodeBoundary, rect, result);
      break;
    case 'open':
      openRect(input, opts.se, erodeBoundary, dilateBoundary, rect, result);
      break;
    case 'close':
      closeRect(input, opts.se, dilateBoundary, erodeBoundary, rect, result);
      break;
  }
  applyLabel(result, input, opts.label, rect);
  return result;
}

export function dilate(input: GrayImage, opts: LocalOpOptions, out?: GrayImage): GrayImage {
  return applyLocal('dilate', input, opts, out);
}

export function erode(input: GrayImage, opts: LocalOpOptions, out?: GrayImage): GrayImage {
  return applyLocal('erode', input, opts, out);
}

export function open(input: GrayImage, opts: LocalOpOptions, out?: GrayImage): GrayImage {
  return applyLocal('open', input, opts, out);
}

export function close(input: GrayImage, opts: LocalOpOptions, out?: GrayImage): GrayImage {
  return applyLocal('close', input, opts, out);
}
