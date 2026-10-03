/**
 * The built-in replayable sample: builds a self-made RGBA terrain, decodes it
 * by hand, then walks through seed add/move/withdraw and height edits,
 * including a plateau bridge removal that forces re-competition.
 */
import { WatershedEngine } from './engine.ts';
import { decodeRgbaToHeights, sampleTerrainRgba } from './rgba.ts';
import { replay, type Op } from './ops.ts';

export function sampleOps(): Op[] {
  return [
    { op: 'comment', text: 'seed two basins' },
    { op: 'addSeed', id: 'A', x: 8, y: 8 },
    { op: 'addSeed', id: 'B', x: 24, y: 16 },
    { op: 'comment', text: 'claim part of the plateau band with a third seed' },
    { op: 'addSeed', id: 'C', x: 16, y: 12 },
    { op: 'comment', text: 'move C across the plateau: territory is re-competed' },
    { op: 'moveSeed', id: 'C', x: 24, y: 12 },
    { op: 'comment', text: 'carve a channel through the plateau band (cross-tile effect)' },
    { op: 'setHeights', edits: [{ x: 10, y: 11, h: 20 }, { x: 10, y: 12, h: 20 }, { x: 10, y: 13, h: 20 }] },
    { op: 'comment', text: 'withdraw the bridging seed C: A and B re-compete its region' },
    { op: 'removeSeed', id: 'C' },
    { op: 'comment', text: 'budget-limited edit: pause mid-job, then resume' },
    { op: 'pauseBudget', budget: 5 },
    { op: 'setHeights', edits: [{ x: 22, y: 6, h: 200 }] },
    { op: 'resume' },
  ];
}

export function runSample(randomize?: (arr: number[]) => void): string {
  const { buf, width, height } = sampleTerrainRgba();
  const heights = decodeRgbaToHeights(buf, width, height);
  const engine = new WatershedEngine(width, height, heights, { tileSize: 8, randomize });
  const lines = [`image ${width}x${height} tile=8`, ...replay(engine, sampleOps())];
  return lines.join('\n');
}
