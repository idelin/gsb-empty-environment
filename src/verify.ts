import type { EventLog } from './model.ts';
import { sliceLog } from './slicer.ts';
import type { SliceResult } from './slicer.ts';
import { replaySlice } from './replay.ts';
import type { ReplayResult } from './replay.ts';

export interface VerificationReport {
  slice: SliceResult;
  replay: ReplayResult;
  ok: boolean;
  unverifiable: boolean;
}

export function verifySlice(log: EventLog, criterionId: number): VerificationReport {
  const slice = sliceLog(log, criterionId);
  const replay = replaySlice(slice.events, criterionId);
  const unverifiable =
    slice.gaps.length > 0 || replay.gaps.length > 0 || replay.assertion === undefined;
  const ok =
    !unverifiable &&
    replay.mismatches.length === 0 &&
    replay.assertion !== undefined &&
    replay.assertion.match;
  return { slice, replay, ok, unverifiable };
}
