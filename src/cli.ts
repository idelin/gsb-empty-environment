import { readFileSync, writeFileSync } from 'node:fs';
import { basename } from 'node:path';
import { parseScript, validateScript } from './validate.ts';
import { replay } from './simulator.ts';
import { ReplayLog } from './replay-log.ts';
import { shrink, ShrinkError } from './shrinker.ts';

const USAGE = `Usage:
  node src/cli.ts shrink <script.json> [--out <shrunk.json>] [--log <replays.jsonl>]
  node src/cli.ts replay <script.json>

shrink  Reduce a failing protocol script to a 1-minimal script that still
        triggers the assertion failure. Every replay is summarized to the
        JSONL log (default: <script>.replays.jsonl). Candidates that are not
        executable are recorded but never accepted.
replay  Replay a script once and print the outcome.

Exit codes: 0 ok | 2 invalid script | 3 script does not fail (shrink only)`;

function parseFlags(args: string[]): { flags: Map<string, string>; positional: string[] } {
  const flags = new Map<string, string>();
  const positional: string[] = [];
  for (let i = 0; i < args.length; i += 1) {
    if (args[i].startsWith('--')) {
      const value = args[i + 1];
      if (value === undefined || value.startsWith('--')) {
        throw new ShrinkError(`missing value for ${args[i]}`);
      }
      flags.set(args[i], value);
      i += 1;
    } else {
      positional.push(args[i]);
    }
  }
  return { flags, positional };
}

function defaultPath(scriptPath: string, suffix: string): string {
  return scriptPath.replace(/\.json$/, '') + suffix;
}

export function main(argv: string[]): number {
  const [command, ...rest] = argv;
  if (command !== 'shrink' && command !== 'replay') {
    console.error(USAGE);
    return 1;
  }
  const { flags, positional } = parseFlags(rest);
  const scriptPath = positional[0];
  if (!scriptPath) {
    console.error(USAGE);
    return 1;
  }

  const { script, errors } = parseScript(readFileSync(scriptPath, 'utf8'));
  if (!script) {
    console.error(`error: ${basename(scriptPath)} is not executable:`);
    for (const error of errors) console.error(`  - ${error}`);
    return 2;
  }

  if (command === 'replay') {
    const result = replay(script);
    if (result.outcome === 'failed') {
      console.log(`FAILED at step "${result.failure.stepId}": ${result.failure.message}`);
    } else {
      console.log(`passed (${result.stepsExecuted} steps executed)`);
    }
    return 0;
  }

  const outPath = flags.get('--out') ?? defaultPath(scriptPath, '.shrunk.json');
  const logPath = flags.get('--log') ?? defaultPath(scriptPath, '.replays.jsonl');
  const log = new ReplayLog(logPath);
  try {
    const result = shrink(script, log);
    writeFileSync(outPath, `${JSON.stringify(result.script, null, 2)}\n`);
    console.log(
      `shrunk ${script.steps.length} -> ${result.script.steps.length} steps ` +
        `(${result.replayCount} replays logged to ${logPath})`,
    );
    console.log(`shrunk script written to ${outPath}`);
    return 0;
  } catch (error) {
    if (error instanceof ShrinkError) {
      console.error(`error: ${error.message}`);
      return error.message.startsWith('input script is not executable') ? 2 : 3;
    }
    throw error;
  }
}

if (import.meta.url === `file://${process.argv[1]}`) {
  process.exit(main(process.argv.slice(2)));
}

export { validateScript };
