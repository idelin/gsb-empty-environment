/**
 * Worst-case error analysis for the fixed-point gain interpolator.
 *
 * The library normalizes each frame to unit total energy:
 *     sum_i gain_i^2 = 1.
 * Fixed-point rounding makes the achieved energy deviate from 1. This module
 * computes a rigorous upper bound on that deviation for a given
 * configuration and reports whether a requested tolerance is reachable.
 *
 * Error model (all quantities in normalized output units, ulp = 2^-outFracBits):
 *
 *   - The interpolated gains G_i are exact integers; normalization computes
 *         S ~= 2^(fracBits+outFracBits) / sqrt(E),  E = sum G_i^2
 *     The relative error of S is bounded by
 *         delta = delta_isqrt + delta_div
 *         delta_isqrt <= 1 / (2^guardBits * sqrt(E_min))
 *         delta_div   <= 1 / (2 * S_min),  S_min = 2^outFracBits / sqrt(N)
 *   - After scaling, sum h_i^2 = (1 + delta)^2 with h_i = G_i * S / 2^f.
 *     Rounding each h_i to the output grid contributes at most
 *         2^-outFracBits * sqrt(N) * (1 + delta)   (Cauchy-Schwarz)
 *     plus the quadratic residue N * 2^(-2*outFracBits) / 4.
 *
 *   total energy error <= 2*delta + delta^2
 *                       + 2^-outFracBits * sqrt(N) * (1 + delta)
 *                       + N * 2^(-2*outFracBits) / 4
 *
 * Because a single least-significant-bit gain cannot be normalized
 * accurately in any fixed-point format, the bound is stated for frames whose
 * interpolated energy is at least `minGain^2` (normalized units). Frames
 * below that energy are not covered by the guarantee.
 */
import { InterpolatorConfig } from "./interpolator";

export interface AnalysisConfig extends InterpolatorConfig {
  /**
   * Smallest nonzero interpolated gain (normalized, 0..1] that the
   * application guarantees. Frames whose interpolated gains are all below
   * this magnitude are excluded from the energy-error guarantee.
   */
  minGain: number;
}

export interface AnalysisReport {
  /** True when energyErrorBound <= tolerance. */
  feasible: boolean;
  /** Requested maximum total energy error. */
  tolerance: number;
  /** Rigorous worst-case bound on |sum gain_i^2 - 1| per frame. */
  energyErrorBound: number;
  /** Individual contributions to the bound. */
  terms: {
    /** 2*delta + delta^2: relative error of the normalization scale factor. */
    scaleFactor: number;
    /** Part of delta from the integer square root. */
    isqrt: number;
    /** Part of delta from the final rounding of the scale factor. */
    scaleRounding: number;
    /** Output-grid rounding of the normalized gains. */
    outputRounding: number;
    /** Quadratic residue of the output rounding. */
    roundingQuadratic: number;
  };
  /** Human-readable explanations, populated when infeasible. */
  reasons: string[];
  /** Actionable suggestions to reach the tolerance. */
  suggestions: string[];
  /** Informational internal word widths (bits) required by the config. */
  bitWidths: {
    interpolatedGain: number;
    energy: number;
    rootOperand: number;
    scaleFactor: number;
    product: number;
  };
}

export function analyzeConfiguration(
  config: AnalysisConfig,
  tolerance: number
): AnalysisReport {
  const { numSpeakers, fracBits, outFracBits, guardBits, minGain } = config;
  const reasons: string[] = [];
  const suggestions: string[] = [];

  if (!(tolerance > 0)) {
    throw new Error("tolerance must be positive");
  }
  if (!(minGain > 0) || minGain > 1) {
    throw new Error("minGain must be in (0, 1]");
  }

  const n = numSpeakers;
  const sqrtN = Math.sqrt(n);

  // Smallest interpolated gain considered, in LSBs of the gain format.
  const minGainLsb = minGain * 2 ** fracBits;
  // Smallest covered energy E_min = minGainLsb^2 (single active speaker).
  const sqrtEmin = minGainLsb;

  // Relative error contributions of the scale factor S.
  const deltaIsqrt = 1 / (2 ** guardBits * sqrtEmin);
  const sMin = 2 ** outFracBits / sqrtN;
  const deltaDiv = 1 / (2 * sMin);
  const delta = deltaIsqrt + deltaDiv;

  const scaleFactorTerm = 2 * delta + delta * delta;
  const outputRoundingTerm = 2 ** -outFracBits * sqrtN * (1 + delta);
  const quadraticTerm = (n * 2 ** (-2 * outFracBits)) / 4;

  const energyErrorBound =
    scaleFactorTerm + outputRoundingTerm + quadraticTerm;

  if (minGainLsb < 1) {
    reasons.push(
      `minGain (${minGain}) is below one LSB of the gain format ` +
        `(2^-${fracBits} ≈ ${2 ** -fracBits}); the guarantee is vacuous`
    );
    suggestions.push(
      `increase fracBits above ${Math.ceil(-Math.log2(minGain))} or raise minGain`
    );
  }

  if (energyErrorBound > tolerance) {
    reasons.push(
      `worst-case energy error bound ${energyErrorBound.toExponential(3)} ` +
        `exceeds tolerance ${tolerance.toExponential(3)}`
    );
    // Attribute the dominant term and suggest a remedy.
    const terms: Array<[string, number, string]> = [
      [
        "integer square root",
        deltaIsqrt,
        `increase guardBits (currently ${guardBits}) or raise minGain`,
      ],
      [
        "scale-factor rounding",
        deltaDiv,
        `increase outFracBits (currently ${outFracBits})`,
      ],
      [
        "output-grid rounding",
        outputRoundingTerm,
        `increase outFracBits (currently ${outFracBits})`,
      ],
    ];
    terms.sort((a, b) => b[1] - a[1]);
    reasons.push(`dominant error source: ${terms[0][0]}`);
    suggestions.push(terms[0][2]);
  }

  const gainBits = fracBits + 1;
  const energyBits = 2 * fracBits + Math.ceil(Math.log2(Math.max(n, 1))) + 1;
  const report: AnalysisReport = {
    feasible: reasons.length === 0,
    tolerance,
    energyErrorBound,
    terms: {
      scaleFactor: scaleFactorTerm,
      isqrt: deltaIsqrt,
      scaleRounding: deltaDiv,
      outputRounding: outputRoundingTerm,
      roundingQuadratic: quadraticTerm,
    },
    reasons,
    suggestions,
    bitWidths: {
      interpolatedGain: gainBits,
      energy: energyBits,
      rootOperand: energyBits + 2 * guardBits,
      scaleFactor: fracBits + outFracBits + 1,
      product: gainBits + fracBits + outFracBits + 1,
    },
  };
  return report;
}

/**
 * Exact total energy error of one output frame:
 * |sum_i (frame_i / 2^outFracBits)^2 - 1|, computed with exact integer math.
 * Silent (all-zero) frames have zero error by definition.
 */
export function measureEnergyError(
  frame: readonly number[],
  outFracBits: number
): number {
  let sum = 0n;
  for (const g of frame) {
    sum += BigInt(g) * BigInt(g);
  }
  if (sum === 0n) {
    return 0;
  }
  const shift = BigInt(2 * outFracBits);
  const unit = 1n << shift;
  const diff = sum > unit ? sum - unit : unit - sum;
  return Number(diff) / 2 ** (2 * outFracBits);
}
