import { nominalFps, type FrameRate } from './rate.ts';
import {
  compileRankIndex,
  floorDiv,
  floorMod,
  type DropFrameRule,
  type RankIndex,
} from './dropframe.ts';

// Negative-time policy:
//  - 'reject' (default): negative frames/labels are errors.
//  - 'signed': labels get a leading '-'; mapping is symmetric,
//    frameToLabel(-f) === '-' + frameToLabel(f). The label "-00:00:00:00"
//    (negative zero) is invalid.
export type NegativePolicy = 'reject' | 'signed';

// Label grammar (documented length policy):
//   [-]HH:MM:SS:FF   non-drop
//   [-]HH:MM:SS;FF   drop-frame (';' only accepted when a rule is configured)
// HH is 2 or more digits and grows without bound (no 24h wrap); MM, SS, FF
// are exactly 2 digits; FF must be < nominal fps (which must be 2..99).
const LABEL_RE = /^(-?)(\d{2,}):([0-5]\d):([0-5]\d)([:;])(\d{2})$/;

export type ParseResult =
  | { ok: true; frame: bigint; label: string }
  | { ok: false; kind: 'malformed'; reason: string }
  | {
      ok: false;
      kind: 'dropped-label';
      reason: string;
      nearest: { label: string; frame: bigint; distance: bigint };
    };

export class Timecode {
  readonly rate: FrameRate;
  readonly rule: DropFrameRule | null;
  readonly negative: NegativePolicy;
  readonly fps: bigint; // nominal integer fps used in label fields
  private readonly index: RankIndex | null;

  constructor(opts: { rate: FrameRate; rule?: DropFrameRule | null; negative?: NegativePolicy }) {
    this.rate = opts.rate;
    this.rule = opts.rule ?? null;
    this.negative = opts.negative ?? 'reject';
    this.fps = nominalFps(opts.rate);
    if (this.fps < 2n || this.fps > 99n) {
      throw new RangeError(`nominal fps ${this.fps} unsupported: frame field is exactly 2 digits (2..99)`);
    }
    if (this.rule && this.rule.nominalFps !== this.fps) {
      throw new RangeError(
        `rule "${this.rule.id}" targets nominal ${this.rule.nominalFps} fps but rate nominal is ${this.fps}`,
      );
    }
    this.index = this.rule ? compileRankIndex(this.rule) : null;
  }

  frameToLabel(frame: bigint): string {
    let sign = '';
    let f = frame;
    if (f < 0n) {
      if (this.negative === 'reject') {
        throw new RangeError(`negative frame ${frame} rejected (negative policy: 'reject')`);
      }
      sign = '-';
      f = -f;
    }
    const nominal = this.index ? this.index.rankToNominal(f) : f;
    const ff = floorMod(nominal, this.fps);
    const totalSec = floorDiv(nominal, this.fps);
    const ss = floorMod(totalSec, 60n);
    const totalMin = floorDiv(totalSec, 60n);
    const mm = floorMod(totalMin, 60n);
    const hh = floorDiv(totalMin, 60n);
    const sep = this.rule ? ';' : ':';
    const p2 = (v: bigint): string => v.toString().padStart(2, '0');
    return `${sign}${p2(hh)}:${p2(mm)}:${p2(ss)}${sep}${p2(ff)}`;
  }

  parse(label: string): ParseResult {
    const m = LABEL_RE.exec(label);
    if (!m) {
      return {
        ok: false,
        kind: 'malformed',
        reason: `expected [-]HH:MM:SS:FF (or ';' before FF with a drop-frame rule), got ${JSON.stringify(label)}`,
      };
    }
    const [, sign, hhS, mmS, ssS, sep, ffS] = m;
    if (sep === ';' && !this.rule) {
      return { ok: false, kind: 'malformed', reason: `';' separator used but no drop-frame rule is configured` };
    }
    if (sign === '-' && this.negative === 'reject') {
      return { ok: false, kind: 'malformed', reason: `negative label rejected (negative policy: 'reject')` };
    }
    const ff = BigInt(ffS);
    if (ff >= this.fps) {
      return { ok: false, kind: 'malformed', reason: `frame field ${ffS} >= nominal fps ${this.fps}` };
    }
    const nominal = ((BigInt(hhS) * 60n + BigInt(mmS)) * 60n + BigInt(ssS)) * this.fps + ff;
    if (sign === '-' && nominal === 0n) {
      return { ok: false, kind: 'malformed', reason: 'negative zero label is not allowed' };
    }
    const signed = (v: bigint): bigint => (sign === '-' ? -v : v);
    if (this.index) {
      if (!this.index.isValidNominal(nominal)) {
        const nearest = this.index.nearestValidNominal(nominal);
        const nearestFrame = this.index.nominalToRank(nearest.nominal);
        return {
          ok: false,
          kind: 'dropped-label',
          reason: `label ${JSON.stringify(label)} does not exist under rule "${this.rule!.id}"`,
          nearest: {
            label: this.frameToLabel(signed(nearestFrame)),
            frame: signed(nearestFrame),
            distance: nearest.distance,
          },
        };
      }
      const frame = signed(this.index.nominalToRank(nominal));
      return { ok: true, frame, label: this.frameToLabel(frame) };
    }
    const frame = signed(nominal);
    return { ok: true, frame, label: this.frameToLabel(frame) };
  }

  // Strict variant: throws on malformed labels and on dropped labels
  // (the error message names the nearest legal candidate when one exists).
  frameOf(label: string): bigint {
    const r = this.parse(label);
    if (r.ok) return r.frame;
    const hint = r.kind === 'dropped-label' ? `; nearest legal label is ${r.nearest.label} (frame ${r.nearest.frame})` : '';
    throw new SyntaxError(`invalid timecode label: ${r.reason}${hint}`);
  }

  isValidLabel(label: string): boolean {
    return this.parse(label).ok;
  }
}
