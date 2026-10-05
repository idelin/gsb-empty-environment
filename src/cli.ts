import * as fs from 'fs';
import { Problem } from './types';
import { solve } from './solver';
import { formatSolution } from './report';

interface CliOptions {
  inputPath?: string;
  json: boolean;
  beamWidth?: number;
  maxStops?: number;
}

function parseArgs(argv: string[]): CliOptions {
  const opts: CliOptions = { json: false };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--json') {
      opts.json = true;
    } else if (arg === '--beam-width') {
      opts.beamWidth = Number(argv[++i]);
    } else if (arg === '--max-stops') {
      opts.maxStops = Number(argv[++i]);
    } else if (arg === '--help' || arg === '-h') {
      printUsage();
      process.exit(0);
    } else if (arg !== undefined && !arg.startsWith('--')) {
      opts.inputPath = arg;
    }
  }
  return opts;
}

function printUsage(): void {
  console.log(
    [
      '用法: node dist/cli.js <输入.json> [--json] [--beam-width N] [--max-stops N]',
      '',
      '输入 JSON 格式:',
      '  {',
      '    "depot": { "x": 0, "y": 0 },',
      '    "vehicleCapacity": 10,',
      '    "costPerKm": 1,',
      '    "penaltyPerBike": 50,',
      '    "stations": [',
      '      { "id": "A", "x": 3, "y": 0, "capacity": 20, "initial": 18, "demand": 6 }',
      '    ]',
      '  }',
      '省略输入路径时从标准输入读取。',
    ].join('\n'),
  );
}

function main(): void {
  const opts = parseArgs(process.argv.slice(2));
  const raw =
    opts.inputPath !== undefined
      ? fs.readFileSync(opts.inputPath, 'utf8')
      : fs.readFileSync(0, 'utf8');
  const problem = JSON.parse(raw) as Problem;
  if (opts.beamWidth !== undefined) problem.beamWidth = opts.beamWidth;
  if (opts.maxStops !== undefined) problem.maxStops = opts.maxStops;

  const sol = solve(problem);
  if (opts.json) {
    console.log(JSON.stringify(sol, null, 2));
  } else {
    console.log(formatSolution(problem, sol));
  }
}

main();
