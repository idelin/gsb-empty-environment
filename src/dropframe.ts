// Drop-frame rules as explicit period tables.
//
// A rule never drops real frames. It declares that certain *labels* do not
// exist: within each period of `periodFrames` nominal label positions, the
// offsets in `drops` have no label. Real frames are numbered 0,1,2,... and
// each one carries the next existing label. The compiled RankIndex is the
// bidirectional map between a frame's rank (its continuous frame number) and
// its nominal label position, evaluated by period jumps + binary search, so
// arbitrarily large positions cost O(log period), never a per-frame scan.

export type DropFrameRule = {
  readonly id: string;
  readonly nominalFps: bigint; // nominal integer fps this table applies to
  readonly periodFrames: bigint; // nominal label positions per period
  readonly drops: readonly bigint[]; // sorted, unique, each in [0, periodFrames)
  readonly validPerPeriod: bigint; // periodFrames - drops.length
};

export function makeDropFrameRule(init: {
  id: string;
  nominalFps: bigint | number;
  periodFrames: bigint | number;
  drops: Iterable<bigint | number>;
}): DropFrameRule {
  const period = BigInt(init.periodFrames);
  const fps = BigInt(init.nominalFps);
  if (fps <= 0n) throw new RangeError('nominalFps must be positive');
  if (period <= 0n) throw new RangeError('periodFrames must be positive');
  const drops = [...init.drops].map(BigInt).sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  for (let i = 0; i < drops.length; i++) {
    const d = drops[i];
    if (d < 0n || d >= period) throw new RangeError(`drop offset ${d} outside [0, ${period})`);
    if (i > 0 && drops[i - 1] === d) throw new RangeError(`duplicate drop offset ${d}`);
  }
  const valid = period - BigInt(drops.length);
  if (valid <= 0n) throw new RangeError('rule drops every label in its period');
  return { id: init.id, nominalFps: fps, periodFrames: period, drops, validPerPeriod: valid };
}

// Classic NTSC drop-frame: drop the first `nominalFps/15` frame labels of
// every minute, except every 10th minute. 29.97 (nominal 30) drops 2 labels;
// 59.94 (nominal 60) drops 4.
export function ntscDropFrame(nominalFps: bigint | number = 30n): DropFrameRule {
  const fps = BigInt(nominalFps);
  if (fps % 15n !== 0n || fps <= 0n) {
    throw new RangeError(`NTSC drop-frame needs nominal fps divisible by 15, got ${fps}`);
  }
  const dropCount = fps / 15n;
  const minuteFrames = 60n * fps;
  const drops: bigint[] = [];
  for (let m = 1n; m <= 9n; m++) {
    for (let k = 0n; k < dropCount; k++) drops.push(m * minuteFrames + k);
  }
  return makeDropFrameRule({
    id: `ntsc-df-${fps}`,
    nominalFps: fps,
    periodFrames: 10n * minuteFrames,
    drops,
  });
}

export function floorDiv(a: bigint, b: bigint): bigint {
  const q = a / b;
  return a % b !== 0n && (a < 0n) !== (b < 0n) ? q - 1n : q;
}

export function floorMod(a: bigint, b: bigint): bigint {
  return a - floorDiv(a, b) * b;
}

// First index i with arr[i] >= x (arr sorted ascending).
function lowerBound(arr: readonly bigint[], x: bigint): number {
  let lo = 0;
  let hi = arr.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (arr[mid] < x) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

export type NearestValid = {
  readonly nominal: bigint;
  readonly distance: bigint; // signed: negative = candidate is earlier, 0 = input was valid
};

// Bidirectional rank index: continuous frame number (rank) <-> nominal label
// position. Works over the whole signed integer line; negative ranks address
// labels before the origin symmetrically.
export type RankIndex = {
  readonly rule: DropFrameRule;
  nominalToRank(nominal: bigint): bigint;
  rankToNominal(rank: bigint): bigint;
  isValidNominal(nominal: bigint): boolean;
  nearestValidNominal(nominal: bigint): NearestValid;
};

export function compileRankIndex(rule: DropFrameRule): RankIndex {
  const period = rule.periodFrames;
  const drops = rule.drops;
  const dropCount = BigInt(drops.length);
  const valid = rule.validPerPeriod;

  // Signed count of dropped positions strictly below `nominal`, relative to 0.
  const dropsBelow = (nominal: bigint): bigint => {
    const k = floorDiv(nominal, period);
    const rem = nominal - k * period; // in [0, period)
    return k * dropCount + BigInt(lowerBound(drops, rem));
  };

  const nominalToRank = (nominal: bigint): bigint => nominal - dropsBelow(nominal);

  const rankToNominal = (rank: bigint): bigint => {
    const k = floorDiv(rank, valid);
    const r = rank - k * valid; // in [0, valid)
    // Largest m in [0, period) with m - #{drops < m} <= r. That m is always a
    // valid position, and it is exactly the r-th valid label of the period.
    let lo = 0n;
    let hi = period;
    while (hi - lo > 1n) {
      const mid = (lo + hi) >> 1n;
      if (mid - BigInt(lowerBound(drops, mid)) <= r) lo = mid;
      else hi = mid;
    }
    return k * period + lo;
  };

  const isValidNominal = (nominal: bigint): boolean => {
    const rem = floorMod(nominal, period);
    const i = lowerBound(drops, rem);
    return i >= drops.length || drops[i] !== rem;
  };

  const nearestValidNominal = (nominal: bigint): NearestValid => {
    if (isValidNominal(nominal)) return { nominal, distance: 0n };
    const rank = nominalToRank(nominal);
    const below = rankToNominal(rank - 1n);
    const above = rankToNominal(rank);
    const dBelow = nominal - below;
    const dAbove = above - nominal;
    // Ties resolve to the earlier label (documented policy).
    return dBelow <= dAbove
      ? { nominal: below, distance: -dBelow }
      : { nominal: above, distance: dAbove };
  };

  return { rule, nominalToRank, rankToNominal, isValidNominal, nearestValidNominal };
}
