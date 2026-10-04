#!/usr/bin/env node
import { main } from '../dist/src/cli.js';
try {
  process.exitCode = main(process.argv.slice(2));
} catch (err) {
  console.error('error: ' + (err instanceof Error ? err.message : String(err)));
  process.exitCode = 1;
}
