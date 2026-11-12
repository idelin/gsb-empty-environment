/**
 * OSV SEMVER 区间评估。
 * 规则（参考 OSV-2022-01/OSV 规范的 ECOSYSTEM 版本范围语义）：
 * - 同一 range 的事件按数组顺序依次评估；
 * - introduced（含 0，表示从最早版本起）打开受影响窗口；
 * - fixed 关闭窗口，fixed 版本本身不受影响（边界排除）；
 * - last_affected 关闭窗口，last_affected 版本本身仍受影响（边界包含）；
 * - 预发布版本按 SemVer 规则小于对应正式版。
 */

import { compareSemVer, parseSemVer, type SemVer } from './semver.ts';
import type { OsvAdvisory, OsvAffectedRange, OsvSemverEvent } from './types.ts';

export type RangeVerdict = 'affected' | 'unaffected' | 'unevaluable';

const ZERO_INTRODUCED = '0';

/**
 * 评估单个 range 是否影响给定版本。
 * 遇到无法解析的事件版本时返回 unevaluable，由上层计入“未评估”导致整体不闭合。
 */
export function evaluateRange(version: SemVer, range: OsvAffectedRange): RangeVerdict {
  if (range.type !== undefined && range.type !== 'SEMVER' && range.type !== 'ECOSYSTEM') {
    return 'unevaluable';
  }
  if (!Array.isArray(range.events) || range.events.length === 0) {
    return 'unaffected';
  }

  let windowOpen = false;
  for (const event of range.events) {
    const verdict = applyEvent(version, event, (open) => {
      windowOpen = open;
    });
    if (verdict === 'unevaluable') {
      return 'unevaluable';
    }
  }
  return windowOpen ? 'affected' : 'unaffected';
}

type EventVerdict = 'changed' | 'noop' | 'unevaluable';

function applyEvent(
  version: SemVer,
  event: OsvSemverEvent,
  setWindow: (open: boolean) => void,
): EventVerdict {
  if (event.introduced !== undefined) {
    if (event.introduced === ZERO_INTRODUCED) {
      setWindow(true);
      return 'changed';
    }
    const introduced = parseSemVer(event.introduced);
    if (!introduced) {
      return 'unevaluable';
    }
    if (compareSemVer(version, introduced) >= 0) {
      setWindow(true);
      return 'changed';
    }
    return 'noop';
  }

  if (event.fixed !== undefined) {
    const fixed = parseSemVer(event.fixed);
    if (!fixed) {
      return 'unevaluable';
    }
    /** fixed 边界本身不受影响：版本 < fixed 时窗口仍开启，到达 fixed 即关闭。 */
    if (compareSemVer(version, fixed) < 0) {
      return 'noop';
    }
    setWindow(false);
    return 'changed';
  }

  if (event.last_affected !== undefined) {
    const lastAffected = parseSemVer(event.last_affected);
    if (!lastAffected) {
      return 'unevaluable';
    }
    /** last_affected 边界本身仍受影响：版本 > last_affected 才离开窗口。 */
    if (compareSemVer(version, lastAffected) > 0) {
      setWindow(false);
      return 'changed';
    }
    return 'noop';
  }

  return 'noop';
}

export interface AdvisoryEvaluation {
  affected: boolean;
  /** 存在无法评估的 range（类型不支持或事件版本非法）。 */
  unevaluable: boolean;
  /** 无法评估的具体原因。 */
  unevaluableReason?: 'unsupported-range-type' | 'invalid-advisory-event' | 'invalid-version';
}

/**
 * 评估一份公告对单个版本的影响。
 * 任一 range 命中即受影响；任一 range 不可评估则标记 unevaluable。
 */
export function evaluateAdvisory(versionRaw: string, advisory: OsvAdvisory): AdvisoryEvaluation {
  const version = parseSemVer(versionRaw);
  if (!version) {
    return { affected: false, unevaluable: true, unevaluableReason: 'invalid-version' };
  }

  let affected = false;
  let unevaluable = false;
  let unevaluableReason: AdvisoryEvaluation['unevaluableReason'];
  for (const range of advisory.ranges) {
    if (
      range.type !== undefined &&
      range.type !== 'SEMVER' &&
      range.type !== 'ECOSYSTEM'
    ) {
      unevaluable = true;
      unevaluableReason ??= 'unsupported-range-type';
      continue;
    }
    const verdict = evaluateRange(version, range);
    if (verdict === 'affected') {
      affected = true;
    } else if (verdict === 'unevaluable') {
      unevaluable = true;
      unevaluableReason ??= 'invalid-advisory-event';
    }
  }
  return { affected, unevaluable, unevaluableReason };
}
