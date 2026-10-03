import type { BitWidthPoint, LoadReport } from "./analyzer.ts";

function fmt(n: number, digits = 4): string {
  if (!Number.isFinite(n)) return "inf";
  if (n !== 0 && (Math.abs(n) < 1e-4 || Math.abs(n) >= 1e6)) {
    return n.toExponential(2);
  }
  return n.toFixed(digits);
}

function pFmt(p: number): string {
  if (p === 0) return "<1e-300";
  if (p < 1e-4) return p.toExponential(2);
  return p.toFixed(4);
}

export function formatLoadTable(reports: LoadReport[]): string {
  const header = [
    "hash".padEnd(22),
    "mean".padStart(10),
    "stddev".padStart(10),
    "cv%".padStart(8),
    "cv/theory".padStart(10),
    "min".padStart(9),
    "max".padStart(9),
    "max/min".padStart(9),
    "chi2".padStart(10),
    "coll".padStart(6),
    "verdict".padStart(8),
  ].join(" ");
  const rows = reports.map((r) =>
    [
      r.hashName.padEnd(22),
      r.mean.toFixed(1).padStart(10),
      r.stddev.toFixed(2).padStart(10),
      (r.cv * 100).toFixed(2).padStart(8),
      fmt(r.cvRatio, 2).padStart(10),
      String(r.min).padStart(9),
      String(r.max).padStart(9),
      fmt(r.maxMinRatio, 3).padStart(9),
      r.chi2.toFixed(1).padStart(10),
      String(r.ringCollisions).padStart(6),
      r.verdict.padStart(8),
    ].join(" "),
  );
  return [header, ...rows].join("\n");
}

export function formatBitWidthTable(
  hashName: string,
  points: BitWidthPoint[],
): string {
  const lines = [`  ${hashName}`];
  for (const p of points) {
    const flag =
      p.anomaly === "non-uniform"
        ? "  <-- DEFICIENT (avalanche failure)"
        : p.anomaly === "over-regular"
          ? "  <-- OVER-REGULAR (structural artifact)"
          : "";
    lines.push(
      `    ${String(p.bits).padStart(2)} bits | buckets ${String(p.buckets).padStart(8)}` +
        `| chi2/df ${fmt(p.normalized, 3).padStart(10)}| p ${pFmt(p.pValue).padStart(10)}${flag}`,
    );
  }
  return lines.join("\n");
}

export function summarizeDeficiencies(
  curves: { name: string; points: BitWidthPoint[] }[],
): string {
  const lines: string[] = [];
  for (const { name, points } of curves) {
    const bad = points.filter((p) => p.deficient);
    if (bad.length === 0) {
      lines.push(`  ${name}: uniform at every tested bit width.`);
    } else {
      const widths = bad.map((p) => `${p.bits}(${p.anomaly})`).join(", ");
      const worst = bad.reduce((a, b) =>
        Math.abs(b.normalized - 1) > Math.abs(a.normalized - 1) ? b : a,
      );
      lines.push(
        `  ${name}: AVALANCHE DEFICIENCY at bit width(s) ${widths} ` +
          `(worst: ${worst.bits} bits, chi2/df = ${fmt(worst.normalized, 2)}, ` +
          `p = ${pFmt(worst.pValue)}).`,
      );
    }
  }
  return lines.join("\n");
}
