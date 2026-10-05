#!/usr/bin/env node
// Command line interface:
//   node src/cli.ts compress   <input> <output> [--window-bits N] [--block-size N]
//   node src/cli.ts decompress <input> <output>
//   node src/cli.ts verify     <input>          (round-trip check + size stats)

import { readFileSync, writeFileSync } from 'node:fs';
import { compress, decompress } from './api.ts';
import { inspectBlocks } from './block.ts';

function parseFlags(args: string[]): Map<string, number> {
  const flags = new Map<string, number>();
  for (let i = 0; i < args.length; i++) {
    if (args[i].startsWith('--')) {
      const name = args[i].slice(2);
      const value = Number(args[++i]);
      if (!Number.isFinite(value)) {
        throw new Error(`flag --${name} needs a numeric value`);
      }
      flags.set(name, value);
    }
  }
  return flags;
}

function printStats(label: string, input: number, output: number, blocks: number, ratioLabel: string): void {
  const ratio = input === 0 ? 0 : output / input;
  console.log(`${label}:`);
  console.log(`  input:   ${input} bytes`);
  console.log(`  output:  ${output} bytes`);
  console.log(`  ratio:   ${ratio.toFixed(4)} (${ratioLabel})`);
  console.log(`  blocks:  ${blocks}`);
  console.log('  note: a smaller output is not proof of correctness;');
  console.log('        correctness requires a byte-exact round-trip check.');
}

function main(): void {
  const [cmd, input, output, ...rest] = process.argv.slice(2);
  if (!cmd || !input || (cmd !== 'verify' && !output)) {
    console.error('usage:');
    console.error('  node src/cli.ts compress   <input> <output> [--window-bits N] [--block-size N]');
    console.error('  node src/cli.ts decompress <input> <output>');
    console.error('  node src/cli.ts verify     <input>');
    process.exit(2);
  }
  const flags = parseFlags(rest);

  if (cmd === 'compress') {
    const data = readFileSync(input);
    const windowBits = flags.get('window-bits') ?? 16;
    const blockSize = flags.get('block-size') ?? 1 << 20;
    const out = compress(data, { windowBits, blockSize });
    writeFileSync(output, out);
    printStats('compress', data.length, out.length, inspectBlocks(out).length, 'compressed / original');
  } else if (cmd === 'decompress') {
    const data = readFileSync(input);
    const out = decompress(data);
    writeFileSync(output, out);
    printStats('decompress', data.length, out.length, inspectBlocks(data).length, 'decompressed / compressed');
  } else if (cmd === 'verify') {
    const data = readFileSync(input);
    const windowBits = flags.get('window-bits') ?? 16;
    const blockSize = flags.get('block-size') ?? 1 << 20;
    const packed = compress(data, { windowBits, blockSize });
    const back = decompress(packed);
    const ok = back.length === data.length && back.every((b, i) => b === data[i]);
    printStats('verify', data.length, packed.length, inspectBlocks(packed).length, 'compressed / original');
    if (!ok) {
      console.error('round-trip: FAILED (output differs from input)');
      process.exit(1);
    }
    console.log('round-trip: OK (byte-identical)');
  } else {
    console.error(`unknown command: ${cmd}`);
    process.exit(2);
  }
}

main();
