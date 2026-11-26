import type { DomainId, GenerationId } from './ring.ts';

export interface MaskSpec {
  id: string;
  generation?: GenerationId;
  owner?: DomainId;
  independent?: boolean;
  value?: number;
  fingerprint?: string;
}

export interface VariableLayout {
  secretCount: number;
  masks: MaskSpec[];
}

export function variableCount(layout: VariableLayout): number {
  return layout.secretCount + layout.masks.length;
}

export function maskCoordinate(layout: VariableLayout, maskId: string): number {
  const index = layout.masks.findIndex((mask) => mask.id === maskId);
  if (index < 0) throw new RangeError(`unknown mask id: ${maskId}`);
  return layout.secretCount + index;
}
