import { formatExpr } from "./ast.ts";
import {
  check,
  dimFromUnitExpr,
  minimalFailure,
  renderJudgment,
  type Env,
} from "./check.ts";
import { parseFormula } from "./parser.ts";
import { formatDim, type Dim } from "./units.ts";

const USAGE = `Usage: node src/cli.ts [--base kg,m,s] [--var name=unit]... "formula"

  --base a,b,c   declare base units (default: kg,m,s)
  --var x=m/s    bind variable x to a unit expression over the base units

Examples:
  node src/cli.ts --var v=m/s --var t=s --var a=m/s^2 "v*t + 0.5*a*t^2"
  node src/cli.ts --var v=m/s --var t=s "v + t"
`;

export function run(argv: string[]): number {
  const bases: string[] = [];
  const vars: [string, string][] = [];
  let formula: string | null = null;

  const args = [...argv];
  while (args.length > 0) {
    const arg = args.shift()!;
    if (arg === "--base") {
      const value = args.shift();
      if (!value) return usage(2);
      bases.push(...value.split(",").map((s) => s.trim()).filter(Boolean));
    } else if (arg === "--var") {
      const value = args.shift();
      if (!value || !value.includes("=")) return usage(2);
      const eq = value.indexOf("=");
      vars.push([value.slice(0, eq).trim(), value.slice(eq + 1).trim()]);
    } else if (arg === "--help" || arg === "-h") {
      return usage(0);
    } else {
      formula = arg;
    }
  }
  if (formula === null) return usage(2);

  const effectiveBases = bases.length > 0 ? bases : ["kg", "m", "s"];
  const env: Record<string, Dim> = {};
  for (const [name, unitExpr] of vars) {
    try {
      env[name] = dimFromUnitExpr(unitExpr, effectiveBases);
    } catch (error) {
      console.error((error as Error).message);
      return 2;
    }
  }
  const typedEnv: Env = env;

  let judgment;
  try {
    judgment = check(parseFormula(formula), typedEnv);
  } catch (error) {
    console.error(`parse error: ${(error as Error).message}`);
    return 2;
  }

  console.log(renderJudgment(judgment));
  const failure = minimalFailure(judgment);
  if (failure) {
    console.error(
      `\nerror: ${failure.error}\n  minimal failing subexpression: ${formatExpr(failure.expr)}`,
    );
    return 1;
  }
  console.log(`\n=> ${formatExpr(judgment.expr)} : ${formatDim(judgment.dim!)}`);
  return 0;
}

function usage(code: number): number {
  (code === 0 ? console.log : console.error)(USAGE);
  return code;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  process.exitCode = run(process.argv.slice(2));
}
