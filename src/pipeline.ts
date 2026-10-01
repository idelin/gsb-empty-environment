import { multiply } from './affine.ts';
import { FloatImage, RGBAImage } from './image.ts';
import { fullRect, intersectConvex, intersectRect, unionRect } from './geometry.ts';
import type { Pt, Rect } from './geometry.ts';
import { Stage } from './stages.ts';
import type { AbortCheck, FilterKind } from './stages.ts';

function mergedClip(a: Stage, b: Stage): Pt[] {
  if (a.filter === 'copy') {
    const tx = a.inverse.e;
    const ty = a.inverse.f;
    const shifted: Pt[] = b.srcClip.map((p) => [p[0] + tx, p[1] + ty]);
    return intersectConvex(a.srcClip, shifted);
  }
  const mapped: Pt[] = b.srcClip.map((p) => [
    a.forward.a * p[0] + a.forward.c * p[1] + a.forward.e,
    a.forward.b * p[0] + a.forward.d * p[1] + a.forward.f,
  ]);
  return intersectConvex(a.srcClip, mapped);
}

export function tryMerge(a: Stage, b: Stage): Stage | null {
  if (a.dstW !== b.srcW || a.dstH !== b.srcH) {
    throw new Error('stage sizes do not chain');
  }
  let filter: FilterKind;
  if (a.filter === 'copy' && b.filter === 'copy') {
    filter = 'copy';
  } else if (a.filter === 'copy') {
    filter = b.filter;
  } else if (b.filter === 'copy') {
    filter = a.filter;
  } else {
    return null;
  }
  const fwd = multiply(b.forward, a.forward);
  return new Stage(a.srcW, a.srcH, b.dstW, b.dstH, fwd, filter, mergedClip(a, b));
}

export class Pipeline {
  readonly stages: Stage[];
  private buffers: FloatImage[];
  private inputBuffer: FloatImage | null = null;

  constructor(stages: Stage[]) {
    if (stages.length === 0) throw new Error('pipeline needs at least one stage');
    for (let k = 1; k < stages.length; k++) {
      if (stages[k].srcW !== stages[k - 1].dstW || stages[k].srcH !== stages[k - 1].dstH) {
        throw new Error('stage ' + k + ' input size does not match stage ' + (k - 1) + ' output');
      }
    }
    this.stages = stages.slice();
    this.buffers = stages.map((s) => new FloatImage(s.dstW, s.dstH));
  }

  get srcW(): number {
    return this.stages[0].srcW;
  }

  get srcH(): number {
    return this.stages[0].srcH;
  }

  get dstW(): number {
    return this.stages[this.stages.length - 1].dstW;
  }

  get dstH(): number {
    return this.stages[this.stages.length - 1].dstH;
  }

  private toFloat(input: RGBAImage | FloatImage): FloatImage {
    const img = input instanceof RGBAImage ? FloatImage.fromRGBA(input) : input;
    if (img.width !== this.srcW || img.height !== this.srcH) {
      throw new Error('input size does not match pipeline source size');
    }
    return img;
  }

  render(input: RGBAImage | FloatImage, shouldAbort?: AbortCheck): FloatImage {
    let src = this.toFloat(input);
    this.inputBuffer = src;
    for (let k = 0; k < this.stages.length; k++) {
      const stage = this.stages[k];
      stage.render(src, this.buffers[k], fullRect(stage.dstW, stage.dstH), shouldAbort);
      src = this.buffers[k];
    }
    return this.buffers[this.buffers.length - 1];
  }

  propagateDirty(srcDirty: Rect): (Rect | null)[] {
    const out: (Rect | null)[] = [];
    let d: Rect | null = intersectRect(srcDirty, fullRect(this.srcW, this.srcH));
    out.push(d);
    for (const stage of this.stages) {
      d = d ? stage.dirtyRect(d) : null;
      out.push(d);
    }
    return out;
  }

  renderDirty(
    input: RGBAImage | FloatImage,
    srcDirty: Rect,
    shouldAbort?: AbortCheck,
  ): Rect | null {
    let src = this.toFloat(input);
    this.inputBuffer = src;
    let d: Rect | null = intersectRect(srcDirty, fullRect(this.srcW, this.srcH));
    for (let k = 0; k < this.stages.length; k++) {
      if (!d) return null;
      const stage = this.stages[k];
      const outDirty = stage.dirtyRect(d);
      if (!outDirty) return null;
      stage.render(src, this.buffers[k], outDirty, shouldAbort);
      d = outDirty;
      src = this.buffers[k];
    }
    return d;
  }

  updateStage(index: number, stage: Stage, shouldAbort?: AbortCheck): Rect | null {
    const old = this.stages[index];
    if (!this.inputBuffer) throw new Error('pipeline has not been rendered yet');
    if (stage.srcW !== old.srcW || stage.srcH !== old.srcH || stage.dstW !== old.dstW || stage.dstH !== old.dstH) {
      throw new Error('replacement stage must keep the same sizes');
    }
    const full = fullRect(old.srcW, old.srcH);
    const dOld = old.dirtyRect(full);
    const dNew = stage.dirtyRect(full);
    let d: Rect | null = dOld && dNew ? unionRect(dOld, dNew) : dOld ?? dNew;
    this.stages[index] = stage;
    let src: FloatImage = index === 0 ? this.inputBuffer : this.buffers[index - 1];
    for (let k = index; k < this.stages.length; k++) {
      if (!d) return null;
      const s = this.stages[k];
      s.render(src, this.buffers[k], d, shouldAbort);
      if (k + 1 < this.stages.length) {
        d = s.dirtyRect(d);
      }
      src = this.buffers[k];
    }
    return d;
  }

  output(): FloatImage {
    return this.buffers[this.buffers.length - 1];
  }

  optimize(): Pipeline {
    const merged: Stage[] = [];
    for (const s of this.stages) {
      const last = merged[merged.length - 1];
      if (last) {
        const m = tryMerge(last, s);
        if (m) {
          merged[merged.length - 1] = m;
          continue;
        }
      }
      merged.push(s);
    }
    return new Pipeline(merged);
  }
}
