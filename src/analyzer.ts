import {
  AmbiguityDetector,
  buildFailingSuffix,
} from './ambiguity.ts';
import { runBacktracking } from './interpreter.ts';
import { parsePattern } from './parser.ts';
import type {
  AmbiguityCycle,
  AnalysisOptions,
  AnalysisReport,
  GrowthSample,
} from './types.ts';

/**
 * 分析入口：静态找可泵送歧义环 -> 构造前缀/重复段/失败后缀 ->
 * 用带步数上限的回溯解释器实测分支数增长。
 *
 * 重要边界：本工具只给出“找到证据（confirmed / unverified）”或
 * “未找到证据（no-evidence）”三种结论，任何情况下都不输出“安全”判定。
 */

const DEFAULT_PUMP_COUNTS = [0, 1, 2, 3, 4, 6, 8, 10, 12] as const;
const DEFAULT_STEP_CAP = 1_000_000;
const DEFAULT_RATIO_THRESHOLD = 1.6;
const DEFAULT_MIN_RATIOS = 2;
const DEFAULT_PRODUCT_BUDGET = 200_000;

const DISCLAIMER =
  '本结果仅覆盖“可泵送歧义路径 + 失败后缀”导致的指数回溯。' +
  '未找到证据（no-evidence）不等于正则安全：分析可能受预算、语法子集或检测策略限制，' +
  '且其他复杂度形态（如多项式爆炸、灾难性回溯的其他变体）不在本工具判定范围内。';

interface ResolvedOptions {
  readonly stepCap: number;
  readonly pumpCounts: readonly number[];
  readonly ratioThreshold: number;
  readonly minRatios: number;
  readonly productBudget: number;
}

function resolveOptions(options: AnalysisOptions | undefined): ResolvedOptions {
  return {
    stepCap: options?.stepCap ?? DEFAULT_STEP_CAP,
    pumpCounts: options?.pumpCounts ?? DEFAULT_PUMP_COUNTS,
    ratioThreshold: options?.exponentialRatioThreshold ?? DEFAULT_RATIO_THRESHOLD,
    minRatios: options?.minExponentialRatios ?? DEFAULT_MIN_RATIOS,
    productBudget: options?.productStateBudget ?? DEFAULT_PRODUCT_BUDGET,
  };
}

function measureGrowth(
  source: string,
  witnessPrefix: string,
  pump: string,
  failingSuffix: string,
  options: ResolvedOptions,
): GrowthSample[] {
  const samples: GrowthSample[] = [];

  for (const pumpCount of options.pumpCounts) {
    const input = witnessPrefix + pump.repeat(pumpCount) + failingSuffix;
    const result = runBacktracking(source, input, { stepCap: options.stepCap });
    const previous = samples[samples.length - 1];
    const ratio =
      previous === null || previous === undefined || previous.steps === 0
        ? null
        : result.steps / previous.steps;

    samples.push({
      pumpCount,
      input,
      steps: result.steps,
      capped: result.capped,
      ratioToPrevious: ratio,
    });

    // 已命中步数上限，后续更大泵送只会再次撞顶，采样可以提前结束。
    if (result.capped) {
      break;
    }
  }

  return samples;
}

function judgeExponential(
  samples: readonly GrowthSample[],
  options: ResolvedOptions,
): boolean {
  let qualifiedRatios = 0;

  for (const sample of samples) {
    if (
      sample.ratioToPrevious !== null &&
      sample.ratioToPrevious >= options.ratioThreshold
    ) {
      qualifiedRatios += 1;
    }
  }

  if (qualifiedRatios >= options.minRatios) {
    return true;
  }

  // 撞顶本身是强证据：输入长度仅线性增加，步数却从可完成变为超出固定上限。
  return samples.some((sample) => sample.capped);
}

export function analyzePattern(
  pattern: string | RegExp,
  options: AnalysisOptions = {},
): AnalysisReport {
  const resolved = resolveOptions(options);
  const parsed = parsePattern(pattern);
  const detector = new AmbiguityDetector(parsed, resolved.productBudget);
  const cycle: AmbiguityCycle | null = detector.findAmbiguityCycle();

  if (cycle === null) {
    return {
      source: parsed.source,
      status: 'no-evidence',
      witness: null,
      growth: [],
      reason:
        '乘积图搜索未发现“消费字符且包含分叉”的歧义环，或搜索在预算内未能完成。' +
        '结论为未证实，不能据此宣称该正则安全。',
      disclaimer: DISCLAIMER,
    };
  }

  const { suffix, deadChar } = buildFailingSuffix(detector.getNfa(), cycle.pump);
  const witness = {
    prefix: cycle.prefix,
    pump: cycle.pump,
    failingSuffix: suffix,
    deadChar,
  };

  const growth = measureGrowth(
    parsed.source,
    cycle.prefix,
    cycle.pump,
    suffix,
    resolved,
  );

  const confirmed = judgeExponential(growth, resolved);

  return {
    source: parsed.source,
    status: confirmed ? 'confirmed' : 'unverified',
    witness,
    growth,
    reason: confirmed
      ? '回溯解释器实测步数随泵送次数呈超线性（指数特征）增长，或在固定步数上限处被截断。'
      : '静态分析找到了可泵送歧义环，但当前泵送规模与步数预算下未观察到达到阈值的指数增长；' +
        '结论为未证实而非安全。',
    disclaimer: DISCLAIMER,
  };
}
