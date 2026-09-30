// Shared harness: run a trace through the arbiter, recording per-step states
// and outputs for the independent invariant checker.

import { GestureArbiter, type ArbiterState } from './arbiter.ts';
import { SpaceConverter, type Affine } from './geometry.ts';
import { layoutTree, type GlyphMetricsFixture, type NodeFixture } from './layout.ts';
import type { GestureEvent, Thresholds } from './types.ts';
import type { NamedTrace } from './traceGen.ts';
import type { StepRecord } from './invariants.ts';

export interface SceneFixture {
  spaces: Record<string, Affine>;
  glyphMetrics: GlyphMetricsFixture;
  tree: NodeFixture;
}

export interface TraceRun {
  steps: StepRecord[];
  outputs: GestureEvent[];
  arbiter: GestureArbiter;
}

export function makeArbiter(fixture: SceneFixture, thresholds?: Partial<Thresholds>): GestureArbiter {
  const converter = new SpaceConverter('root');
  for (const [space, affine] of Object.entries(fixture.spaces)) {
    if (space !== 'root') converter.register(space, affine);
  }
  const sceneRoot = layoutTree(fixture.tree, fixture.glyphMetrics);
  return new GestureArbiter({ converter, sceneRoot, ...(thresholds ? { thresholds } : {}) });
}

export function runTrace(arbiter: GestureArbiter, trace: NamedTrace): TraceRun {
  const steps: StepRecord[] = [];
  const outputs: GestureEvent[] = [];
  for (const op of trace.ops) {
    const stateBefore: ArbiterState = arbiter.state;
    let emitted: GestureEvent[];
    if (op.kind === 'event') {
      emitted = arbiter.dispatch(op.event).events;
    } else {
      emitted = arbiter.tick(op.time);
    }
    outputs.push(...emitted);
    steps.push({ stateBefore, stateAfter: arbiter.state, outputs: emitted });
  }
  return { steps, outputs, arbiter };
}
