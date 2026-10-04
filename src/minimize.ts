import type { TraceEvent, Claim, Evidence } from './types';
import { indexById } from './graph';
import { verifyEvidence } from './verifier';

export function compareIdLists(a: string[], b: string[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] < b[i]) return -1;
    if (a[i] > b[i]) return 1;
  }
  return a.length - b.length;
}

export function* combinations(sortedIds: string[], k: number): Generator<string[]> {
  const n = sortedIds.length;
  if (k < 0 || k > n) return;
  const idx: number[] = [];
  for (let i = 0; i < k; i++) idx.push(i);
  while (true) {
    const combo: string[] = [];
    for (let i = 0; i < k; i++) combo.push(sortedIds[idx[i]]);
    yield combo;
    let i = k - 1;
    while (i >= 0 && idx[i] === n - k + i) i--;
    if (i < 0) return;
    idx[i]++;
    for (let j = i + 1; j < k; j++) idx[j] = idx[j - 1] + 1;
  }
}

// Enumerates subsets of the candidate closure in minimization order
// (fewer events first, then lexicographic on the sorted id list) and returns
// the first subset the independent verifier still accepts. That first hit is
// provably minimal because the enumeration order is total. If the verifier
// call budget runs out first, the full closure (always verified up front) is
// returned flagged as not-proven-minimal.
export function minimizeEvidence(
  allEvents: TraceEvent[],
  candidateIds: string[],
  claim: Claim,
  budget: number
): Evidence {
  const index = indexById(allEvents);
  const candidates = candidateIds.slice().sort();
  const toEvents = (ids: string[]): TraceEvent[] => {
    const out: TraceEvent[] = [];
    for (const id of ids) {
      const e = index.get(id);
      if (e) out.push(e);
    }
    return out;
  };
  const full = verifyEvidence(toEvents(candidates), claim);
  let calls = 0;
  for (let k = 1; k <= candidates.length; k++) {
    for (const combo of combinations(candidates, k)) {
      let hasFocus = true;
      for (const f of claim.focus) {
        if (combo.indexOf(f) < 0) {
          hasFocus = false;
          break;
        }
      }
      if (!hasFocus) continue;
      if (calls >= budget) {
        return {
          eventIds: candidates,
          events: toEvents(candidates),
          minimal: false,
          budgetExhausted: true,
          verified: full.ok,
        };
      }
      calls++;
      if (verifyEvidence(toEvents(combo), claim).ok) {
        return { eventIds: combo, events: toEvents(combo), minimal: true, budgetExhausted: false, verified: true };
      }
    }
  }
  return {
    eventIds: candidates,
    events: toEvents(candidates),
    minimal: false,
    budgetExhausted: false,
    verified: full.ok,
  };
}
