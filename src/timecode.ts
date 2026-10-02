// Timecode labels <-> continuous frame numbers, compiled from an explicit
// drop-frame period table. Drop-frame is a *labelling* scheme: no real
// frames are ever dropped. The compile step builds a bidirectional rank
// index so label <-> frame conversion jumps whole periods (BigInt
// division) instead of scanning frame by frame.
//
// Policies (fixed, documented):
//   - Negative time is not supported. Frame numbers and labels start at
//     0 == 00:00:00:00. Negative inputs raise RangeError / InvalidLabelError.
//   - Label shape: HH:MM:SS<sep>FF. HH is 2+ digits (unbounded hours),
//     MM/SS exactly 2 digits and < 60, FF width = digits of nominalFps-1
//     (minimum 2). sep is ";" iff the rule drops labels, else ":".
//   - Labels that the drop table excludes do not exist: strict conversion
//     throws InvalidLabelError; nearestValidLabel() / parseLabelOrNearest()
//     return the closest legal candidate (ties resolve forward).

import { rat, roundToBigInt, type Rational, type RoundingMode } from "./rational.ts";

export interface DropRuleSpec {
  readonly nominalFps: number; // integer label frames per second, e.g. 30
  readonly periodSeconds: number; // drop table period, e.g. 60 (one minute)
  readonly dropFrameLabels: readonly number[]; // frame labels dropped at each dropping period start, e.g. [0, 1]
  readonly keepEveryPeriods?: number; // every K-th period keeps all labels (NTSC: 10); omit = every period drops
}

export interface CompiledRule {
  readonly name: string;
  readonly nominalFps: bigint;
  readonly periodSeconds: bigint;
  readonly keepEveryPeriods: bigint; // 0n = no exemption period
  readonly fps: Rational; // actual frame rate, e.g. 30000/1001
  readonly framesPerPeriod: bigint;
  readonly dropPositions: readonly bigint[]; // sorted, within-period label positions that do not exist
  readonly validPerDroppingPeriod: bigint;
  readonly periodsPerSuper: bigint; // exemption cycle length in periods (1 = no exemption)
  readonly validPerSuperPeriod: bigint; // valid labels per exemption cycle
  readonly ffWidth: number;
}

export interface Label {
  readonly h: bigint;
  readonly m: bigint;
  readonly s: bigint;
  readonly f: bigint;
}

export type InvalidReason =
  | "dropped"
  | "bad-format"
  | "bad-separator"
  | "out-of-range"
  | "negative";

export class InvalidLabelError extends Error {
  readonly reason: InvalidReason;
  readonly nearest: { readonly frame: bigint; readonly label: string } | null;
  constructor(reason: InvalidReason, message: string, nearest: { frame: bigint; label: string } | null = null) {
    super(message);
    this.name = "InvalidLabelError";
    this.reason = reason;
    this.nearest = nearest;
  }
}

export function compileRule(name: string, spec: DropRuleSpec, fps: Rational): CompiledRule {
  if (!Number.isInteger(spec.nominalFps) || spec.nominalFps < 1) {
    throw new RangeError("nominalFps must be a positive integer");
  }
  if (!Number.isInteger(spec.periodSeconds) || spec.periodSeconds < 1) {
    throw new RangeError("periodSeconds must be a positive integer");
  }
  if (fps.num <= 0n) throw new RangeError("fps must be positive");
  const drops = [...new Set(spec.dropFrameLabels)].sort((a, b) => a - b);
  for (const d of drops) {
    if (!Number.isInteger(d) || d < 0 || d >= spec.nominalFps) {
      throw new RangeError(`drop frame label ${d} is outside [0, ${spec.nominalFps})`);
    }
  }
  const nominalFps = BigInt(spec.nominalFps);
  const periodSeconds = BigInt(spec.periodSeconds);
  const framesPerPeriod = nominalFps * periodSeconds;
  if (BigInt(drops.length) >= framesPerPeriod) {
    throw new RangeError("drop table would erase an entire period");
  }
  let keepEveryPeriods = 0n;
  if (drops.length > 0 && spec.keepEveryPeriods !== undefined) {
    if (!Number.isInteger(spec.keepEveryPeriods) || spec.keepEveryPeriods < 2) {
      throw new RangeError("keepEveryPeriods must be an integer >= 2");
    }
    keepEveryPeriods = BigInt(spec.keepEveryPeriods);
  }
  const periodsPerSuper = keepEveryPeriods > 0n ? keepEveryPeriods : 1n;
  const validPerDroppingPeriod = framesPerPeriod - BigInt(drops.length);
  const validPerSuperPeriod =
    keepEveryPeriods > 0n
      ? framesPerPeriod + (keepEveryPeriods - 1n) * validPerDroppingPeriod
      : drops.length > 0
        ? validPerDroppingPeriod
        : framesPerPeriod;
  const ffWidth = Math.max(2, String(spec.nominalFps - 1).length);
  return Object.freeze({
    name,
    nominalFps,
    periodSeconds,
    keepEveryPeriods,
    fps,
    framesPerPeriod,
    dropPositions: Object.freeze(drops.map(BigInt)),
    validPerDroppingPeriod,
    periodsPerSuper,
    validPerSuperPeriod,
    ffWidth,
  });
}

function isDroppingPeriod(rule: CompiledRule, p: bigint): boolean {
  return (
    rule.dropPositions.length > 0 &&
    (rule.keepEveryPeriods === 0n || p % rule.keepEveryPeriods !== 0n)
  );
}

function countLessThan(sorted: readonly bigint[], x: bigint): bigint {
  let n = 0n;
  for (const d of sorted) {
    if (d < x) n += 1n;
    else break;
  }
  return n;
}

// Rank of a (valid) within-period label position in the continuous frame
// numbering. This is the compiled bidirectional rank index: O(1) period
// jumps, never a per-frame scan.
function frameIndexOfPosition(rule: CompiledRule, p: bigint, pos: bigint): bigint {
  const superCount = p / rule.periodsPerSuper;
  const j = p % rule.periodsPerSuper;
  let base = superCount * rule.validPerSuperPeriod;
  let rank: bigint;
  if (rule.dropPositions.length === 0) {
    rank = pos;
  } else if (rule.keepEveryPeriods === 0n) {
    rank = pos - countLessThan(rule.dropPositions, pos);
  } else if (j === 0n) {
    rank = pos; // exemption period keeps every label
  } else {
    base += rule.framesPerPeriod + (j - 1n) * rule.validPerDroppingPeriod;
    rank = pos - countLessThan(rule.dropPositions, pos);
  }
  return base + rank;
}

function isDroppedPosition(rule: CompiledRule, p: bigint, pos: bigint): boolean {
  return isDroppingPeriod(rule, p) && rule.dropPositions.includes(pos);
}

function lastValidPosition(rule: CompiledRule, p: bigint): bigint {
  const dropping = isDroppingPeriod(rule, p);
  const count = dropping ? rule.validPerDroppingPeriod : rule.framesPerPeriod;
  let pos = count - 1n;
  if (dropping) {
    for (const d of rule.dropPositions) {
      if (d <= pos) pos += 1n;
      else break;
    }
  }
  return pos;
}

function locate(rule: CompiledRule, h: bigint, m: bigint, s: bigint, f: bigint): { p: bigint; pos: bigint } {
  const totalSeconds = (h * 60n + m) * 60n + s;
  const p = totalSeconds / rule.periodSeconds;
  const within = totalSeconds % rule.periodSeconds;
  return { p, pos: within * rule.nominalFps + f };
}

function nearestValidFrame(rule: CompiledRule, p: bigint, pos: bigint): bigint {
  const ideal = frameIndexOfPosition(rule, p, pos);
  let posF = pos + 1n;
  while (isDroppedPosition(rule, p, posF)) posF += 1n; // always terminates: drops < nominalFps
  const fwd = frameIndexOfPosition(rule, p, posF);
  let bwd = -1n;
  let posB = pos - 1n;
  while (posB >= 0n && isDroppedPosition(rule, p, posB)) posB -= 1n;
  if (posB >= 0n) {
    bwd = frameIndexOfPosition(rule, p, posB);
  } else if (p > 0n) {
    bwd = frameIndexOfPosition(rule, p - 1n, lastValidPosition(rule, p - 1n));
  }
  if (bwd < 0n) return fwd;
  return ideal - bwd < fwd - ideal ? bwd : fwd; // tie resolves forward
}

export function labelToFrame(rule: CompiledRule, h: bigint, m: bigint, s: bigint, f: bigint): bigint {
  for (const v of [h, m, s, f]) {
    if (v < 0n) throw new InvalidLabelError("negative", "negative time is not supported");
  }
  if (m >= 60n || s >= 60n) {
    throw new InvalidLabelError("out-of-range", "minutes/seconds must be < 60");
  }
  if (f >= rule.nominalFps) {
    const clamped = frameToLabel(rule, frameIndexOfPosition(rule, locate(rule, h, m, s, 0n).p, locate(rule, h, m, s, rule.nominalFps - 1n).pos));
    throw new InvalidLabelError(
      "out-of-range",
      `frame field ${f} >= nominalFps ${rule.nominalFps}`,
      { frame: labelToFrame(rule, h, m, s, rule.nominalFps - 1n), label: formatLabel(rule, clamped) },
    );
  }
  const { p, pos } = locate(rule, h, m, s, f);
  if (isDroppedPosition(rule, p, pos)) {
    const frame = nearestValidFrame(rule, p, pos);
    throw new InvalidLabelError(
      "dropped",
      `label ${pad(h, 2)}:${pad(m)}:${pad(s)};${pad(f, rule.ffWidth)} is excluded by the drop table of rule "${rule.name}"`,
      { frame, label: formatLabel(rule, frameToLabel(rule, frame)) },
    );
  }
  return frameIndexOfPosition(rule, p, pos);
}

export function frameToLabel(rule: CompiledRule, frame: bigint): Label {
  if (frame < 0n) throw new RangeError("negative time is not supported");
  const superCount = frame / rule.validPerSuperPeriod;
  const rem = frame % rule.validPerSuperPeriod;
  let j = 0n;
  let rank: bigint;
  let dropping: boolean;
  if (rule.dropPositions.length === 0 || rule.keepEveryPeriods === 0n) {
    rank = rem;
    dropping = rule.dropPositions.length > 0;
  } else if (rem < rule.framesPerPeriod) {
    rank = rem;
    dropping = false;
  } else {
    const rem2 = rem - rule.framesPerPeriod;
    j = 1n + rem2 / rule.validPerDroppingPeriod;
    rank = rem2 % rule.validPerDroppingPeriod;
    dropping = true;
  }
  let pos = rank;
  if (dropping) {
    for (const d of rule.dropPositions) {
      if (d <= pos) pos += 1n;
      else break;
    }
  }
  const p = superCount * rule.periodsPerSuper + j;
  const within = pos / rule.nominalFps;
  const f = pos % rule.nominalFps;
  const totalSeconds = p * rule.periodSeconds + within;
  return {
    h: totalSeconds / 3600n,
    m: (totalSeconds / 60n) % 60n,
    s: totalSeconds % 60n,
    f,
  };
}

function pad(v: bigint, width: number = 2): string {
  return v.toString().padStart(width, "0");
}

export function formatLabel(rule: CompiledRule, label: Label): string {
  const sep = rule.dropPositions.length > 0 ? ";" : ":";
  return `${pad(label.h, 2)}:${pad(label.m)}:${pad(label.s)}${sep}${pad(label.f, rule.ffWidth)}`;
}

export function frameToTimecode(rule: CompiledRule, frame: bigint): string {
  return formatLabel(rule, frameToLabel(rule, frame));
}

export function parseLabel(rule: CompiledRule, text: string): bigint {
  const wantSep = rule.dropPositions.length > 0 ? ";" : ":";
  const re = new RegExp(`^(\\d{2,}):([0-5]\\d):([0-5]\\d)([:;])(\\d{${rule.ffWidth}})$`);
  const m = re.exec(text);
  if (!m) {
    throw new InvalidLabelError(
      "bad-format",
      `label "${text}" does not match HH:MM:SS${wantSep}FF (HH 2+ digits, MM/SS 2 digits < 60, FF ${rule.ffWidth} digits)`,
    );
  }
  if (m[4] !== wantSep) {
    throw new InvalidLabelError(
      "bad-separator",
      `rule "${rule.name}" ${rule.dropPositions.length > 0 ? "drops labels and requires ';'" : "drops nothing and requires ':'"} before the frame field`,
    );
  }
  return labelToFrame(rule, BigInt(m[1]), BigInt(m[2]), BigInt(m[3]), BigInt(m[5]));
}

// Invalid (dropped) labels resolve to the nearest legal candidate instead of throwing.
export function parseLabelOrNearest(
  rule: CompiledRule,
  text: string,
): { frame: bigint; label: string; corrected: boolean } {
  try {
    const frame = parseLabel(rule, text);
    return { frame, label: frameToTimecode(rule, frame), corrected: false };
  } catch (e) {
    if (e instanceof InvalidLabelError && e.reason === "dropped" && e.nearest) {
      return { frame: e.nearest.frame, label: e.nearest.label, corrected: true };
    }
    throw e;
  }
}

// Exact start time of a frame in seconds.
export function frameToSeconds(rule: CompiledRule, frame: bigint): Rational {
  if (frame < 0n) throw new RangeError("negative time is not supported");
  return rat(frame * rule.fps.den, rule.fps.num);
}

// frame -> sample: exact value is frame * sampleRate / fps; rounded with an
// explicit caller-supplied mode. No floats involved at any point.
export function frameToSampleExact(rule: CompiledRule, frame: bigint, sampleRate: bigint): Rational {
  if (sampleRate <= 0n) throw new RangeError("sampleRate must be positive");
  return rat(frame * sampleRate * rule.fps.den, rule.fps.num);
}

export function frameToSample(rule: CompiledRule, frame: bigint, sampleRate: bigint, mode: RoundingMode): bigint {
  return roundToBigInt(frameToSampleExact(rule, frame, sampleRate), mode);
}

// sample -> frame: exact value is sample * fps / sampleRate.
export function sampleToFrameExact(rule: CompiledRule, sample: bigint, sampleRate: bigint): Rational {
  if (sampleRate <= 0n) throw new RangeError("sampleRate must be positive");
  if (sample < 0n) throw new RangeError("negative time is not supported");
  return rat(sample * rule.fps.num, sampleRate * rule.fps.den);
}

export function sampleToFrame(rule: CompiledRule, sample: bigint, sampleRate: bigint, mode: RoundingMode): bigint {
  return roundToBigInt(sampleToFrameExact(rule, sample, sampleRate), mode);
}
