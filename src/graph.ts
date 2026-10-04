import { StructuringElement } from './se.ts';
import type { BoundaryMode, LabelMask } from './types.ts';
import { localOpReach, type LocalOp } from './ops.ts';

export interface LocalStageSpec {
  kind: 'local';
  name: string;
  op: LocalOp;
  input: string; // source name or earlier stage name
  se: StructuringElement;
  boundary?: BoundaryMode; // default 'zero'
  labels?: LabelMask; // only these labels are written; others pass through
}

export interface ReconstructStageSpec {
  kind: 'reconstruct';
  name: string;
  marker: string; // marker input (source or earlier stage)
  mask: string; // mask input (source or earlier stage)
  se: StructuringElement;
  boundary?: BoundaryMode;
  labels?: LabelMask;
  maxIterations?: number; // fixed-point budget; default unbounded
}

export type StageSpec = LocalStageSpec | ReconstructStageSpec;

export interface GraphSpec {
  stages: StageSpec[];
  output?: string; // defaults to the last stage
}

export interface CompiledLocalStage {
  kind: 'local';
  name: string;
  op: LocalOp;
  input: string;
  se: StructuringElement;
  boundary: BoundaryMode;
  labels?: LabelMask;
  rx: number;
  ry: number;
}

export interface CompiledReconstructStage {
  kind: 'reconstruct';
  name: string;
  marker: string;
  mask: string;
  se: StructuringElement;
  boundary: BoundaryMode;
  labels?: LabelMask;
  maxIterations: number;
}

export type CompiledStage = CompiledLocalStage | CompiledReconstructStage;

export interface CompiledGraph {
  stages: CompiledStage[];
  output: string;
}

/**
 * Compile a multi-stage operation graph: validates the DAG (stages must
 * reference only sources or earlier stages), resolves defaults, and
 * precomputes per-stage influence radii used for dirty-region tracking.
 */
export function compileGraph(spec: GraphSpec, sourceNames: string[]): CompiledGraph {
  const defined = new Set<string>(sourceNames);
  const names = new Set<string>();
  const stages: CompiledStage[] = [];
  for (const s of spec.stages) {
    if (names.has(s.name)) throw new Error(`duplicate stage name '${s.name}'`);
    names.add(s.name);
    const need = s.kind === 'local' ? [s.input] : [s.marker, s.mask];
    for (const n of need) {
      if (!defined.has(n) && !names.has(n)) {
        throw new Error(`stage '${s.name}' references unknown input '${n}' (must be a source or an earlier stage)`);
      }
    }
    if (s.kind === 'local') {
      const { rx, ry } = localOpReach(s.op, s.se);
      stages.push({
        kind: 'local',
        name: s.name,
        op: s.op,
        input: s.input,
        se: s.se,
        boundary: s.boundary ?? 'zero',
        labels: s.labels,
        rx,
        ry,
      });
    } else {
      stages.push({
        kind: 'reconstruct',
        name: s.name,
        marker: s.marker,
        mask: s.mask,
        se: s.se,
        boundary: s.boundary ?? 'zero',
        labels: s.labels,
        maxIterations: s.maxIterations ?? Number.POSITIVE_INFINITY,
      });
    }
  }
  const output = spec.output ?? spec.stages[spec.stages.length - 1]?.name;
  if (!output || !names.has(output)) throw new Error(`unknown output stage '${output}'`);
  return { stages, output };
}
