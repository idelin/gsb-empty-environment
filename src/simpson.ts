/**
 * Simpson's paradox construction and group-contribution explanation.
 *
 * An arm's overall rate is exactly the weight-sum of its group rates:
 *
 *   overall = sum_g (den_g / den_total) * rate_g
 *
 * so a reversal is fully explained by how each arm's denominator mass
 * is distributed across groups with different base rates. All weights,
 * rates and contributions here are exact rationals.
 */

import { cmp, mul, rational, type Rational } from './rational.ts';

export interface SimpsonGroupInput {
  readonly group: string;
  readonly numerator: bigint;
  readonly denominator: bigint;
}

export interface SimpsonArmInput {
  readonly arm: string;
  readonly groups: readonly SimpsonGroupInput[];
}

export interface ArmGroupBreakdown {
  readonly group: string;
  readonly rate: Rational;
  /** den_g / den_total for this arm */
  readonly weight: Rational;
  /** rate * weight: exact contribution of this group to the overall rate */
  readonly contribution: Rational;
}

export interface ArmAnalysis {
  readonly arm: string;
  readonly overall: Rational;
  readonly groups: readonly ArmGroupBreakdown[];
}

export interface SimpsonAnalysis {
  readonly arms: readonly ArmAnalysis[];
  /** winner (highest rate) per group, 'tie' when equal */
  readonly groupWinners: Readonly<Record<string, string>>;
  /** winner by overall aggregated rate, 'tie' when equal */
  readonly overallWinner: string;
  /** true when every group winner differs from the overall winner */
  readonly reversal: boolean;
  /** human-readable explanation of the contributions and any reversal */
  readonly narrative: readonly string[];
}

function winnerOf(entries: readonly { name: string; r: Rational }[]): string {
  let best = entries[0];
  let tie = false;
  for (const entry of entries.slice(1)) {
    const order = cmp(entry.r, best.r);
    if (order > 0) {
      best = entry;
      tie = false;
    } else if (order === 0) {
      tie = true;
    }
  }
  return tie ? 'tie' : best.name;
}

export function simpsonAnalysis(arms: readonly SimpsonArmInput[]): SimpsonAnalysis {
  if (arms.length < 2) {
    throw new RangeError('simpsonAnalysis: at least two arms are required');
  }
  const groupNames = arms[0].groups.map((g) => g.group);
  for (const arm of arms) {
    const names = arm.groups.map((g) => g.group);
    if (names.length !== groupNames.length || !names.every((n, i) => n === groupNames[i])) {
      throw new Error('simpsonAnalysis: all arms must share the same groups in the same order');
    }
  }

  const analyses: ArmAnalysis[] = arms.map((arm) => {
    const denTotal = arm.groups.reduce((acc, g) => acc + g.denominator, 0n);
    const numTotal = arm.groups.reduce((acc, g) => acc + g.numerator, 0n);
    const groups: ArmGroupBreakdown[] = arm.groups.map((g) => {
      const rate = rational(g.numerator, g.denominator);
      const weight = rational(g.denominator, denTotal);
      return { group: g.group, rate, weight, contribution: mul(rate, weight) };
    });
    return { arm: arm.arm, overall: rational(numTotal, denTotal), groups };
  });

  const groupWinners: Record<string, string> = {};
  for (let i = 0; i < groupNames.length; i += 1) {
    groupWinners[groupNames[i]] = winnerOf(
      analyses.map((a) => ({ name: a.arm, r: a.groups[i].rate })),
    );
  }
  const overallWinner = winnerOf(analyses.map((a) => ({ name: a.arm, r: a.overall })));
  const reversal =
    overallWinner !== 'tie' &&
    groupNames.every((g) => groupWinners[g] !== 'tie' && groupWinners[g] !== overallWinner);

  const narrative: string[] = [];
  for (const analysis of analyses) {
    const parts = analysis.groups
      .map(
        (g) =>
          `${g.group}: rate ${g.rate.num}/${g.rate.den} x weight ${g.weight.num}/${g.weight.den}`,
      )
      .join('; ');
    narrative.push(
      `arm ${analysis.arm}: overall ${analysis.overall.num}/${analysis.overall.den} = sum of group contributions (${parts})`,
    );
  }
  if (reversal) {
    narrative.push(
      `Simpson reversal: ${overallWinner} wins overall but loses every group; ` +
        'the arms concentrate denominator mass in different groups, so the ' +
        'weight x rate mix flips the aggregate ranking.',
    );
  }

  return { arms: analyses, groupWinners, overallWinner, reversal, narrative };
}

/**
 * Classic kidney-stone dataset: treatment A beats treatment B in both
 * stone-size groups, yet loses overall.
 */
export function kidneyStoneCase(): readonly SimpsonArmInput[] {
  return [
    {
      arm: 'A',
      groups: [
        { group: 'small', numerator: 81n, denominator: 87n },
        { group: 'large', numerator: 192n, denominator: 263n },
      ],
    },
    {
      arm: 'B',
      groups: [
        { group: 'small', numerator: 234n, denominator: 270n },
        { group: 'large', numerator: 55n, denominator: 80n },
      ],
    },
  ];
}
