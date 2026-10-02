export function erfc(x: number): number {
  const z = Math.abs(x);
  const t = 1 / (1 + 0.5 * z);
  const polynomial =
    1.00002368 +
    t *
      (0.37409196 +
        t *
          (0.09678418 +
            t *
              (-0.18628806 +
                t *
                  (0.27886807 +
                    t * (-1.13520398 + t * (1.48851587 + t * (-0.82215223 + t * 0.17087277)))))));
  const approximation = t * Math.exp(-z * z - 1.26551223 + t * polynomial);
  return x >= 0 ? approximation : 2 - approximation;
}

export function erf(x: number): number {
  return 1 - erfc(x);
}

export function normalCdf(x: number, mean: number, stdDeviation: number): number {
  return 0.5 * erfc(-(x - mean) / (stdDeviation * Math.SQRT2));
}

export function normalSf(x: number, mean: number, stdDeviation: number): number {
  return 0.5 * erfc((x - mean) / (stdDeviation * Math.SQRT2));
}
