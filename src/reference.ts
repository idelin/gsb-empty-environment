import { GrayImage } from './image.ts';
import { close, dilate, erode, open } from './ops.ts';
import { reconstructDilation } from './reconstruct.ts';
import { compileGraph } from './graph.ts';
import type { StageDef } from './graph.ts';

/**
 * Plain whole-image evaluation of a stage graph: no tiles, no incrementality,
 * unbounded reconstruction budget. Used as the ground-truth reference that
 * the incremental engine is checked against.
 */
export function runReference(
  sources: Record<string, GrayImage>,
  stages: StageDef[],
): Map<string, GrayImage> {
  const env = new Map<string, GrayImage>();
  for (const [name, img] of Object.entries(sources)) {
    env.set(`src:${name}`, img);
  }
  for (const stage of compileGraph(stages)) {
    if (stage.kind === 'reconstruct') {
      const marker = env.get(stage.marker);
      const mask = env.get(stage.mask);
      if (!marker || !mask) throw new Error(`missing inputs for '${stage.id}'`);
      const res = reconstructDilation(marker, mask, {
        connectivity: stage.connectivity,
        label: stage.label ?? null,
      });
      if (!res.converged) throw new Error('reference reconstruction did not converge');
      env.set(stage.id, res.out);
    } else {
      const input = env.get(stage.input);
      if (!input) throw new Error(`missing input for '${stage.id}'`);
      const opts = { se: stage.se, boundary: stage.boundary, label: stage.label ?? null };
      const fn = stage.kind === 'dilate' ? dilate : stage.kind === 'erode' ? erode : stage.kind === 'open' ? open : close;
      env.set(stage.id, fn(input, opts));
    }
  }
  return env;
}
