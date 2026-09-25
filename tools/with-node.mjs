#!/usr/bin/env node
/**
 * Launcher that runs the given arguments with a Node.js version capable of
 * executing TypeScript directly (>= 23.6 natively, or >= 22.6 behind
 * --experimental-strip-types). Useful when the default `node` on PATH is
 * older. Exits non-zero with a clear message if no suitable runtime exists.
 */
import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

const args = process.argv.slice(2);

function capability(version) {
  const [major, minor] = version.split('.').map(Number);
  if (major > 23 || (major === 23 && minor >= 6)) return 'native';
  if (major === 22 && minor >= 6) return 'flag';
  return null;
}

function run(nodePath, extraArgs) {
  const result = spawnSync(nodePath, [...extraArgs, ...args], { stdio: 'inherit' });
  if (result.error) throw result.error;
  process.exit(result.status === null ? 1 : result.status);
}

const current = capability(process.versions.node);
if (current === 'native') run(process.execPath, []);
if (current === 'flag') run(process.execPath, ['--experimental-strip-types']);

const candidates = ['/opt/homebrew/bin/node', '/usr/local/bin/node'];
for (const dir of [join(homedir(), '.nvm/versions/node'), '/usr/local/n/versions/node']) {
  try {
    for (const version of readdirSync(dir)) {
      candidates.push(join(dir, version, 'bin/node'));
    }
  } catch {
    // directory does not exist; ignore
  }
}

let best = null;
for (const candidate of candidates) {
  if (!existsSync(candidate)) continue;
  const probe = spawnSync(candidate, ['-e', 'process.stdout.write(process.versions.node)'], {
    encoding: 'utf8',
  });
  if (probe.status !== 0) continue;
  const cap = capability(probe.stdout.trim());
  if (cap === 'native' || cap === 'flag') {
    const score = probe.stdout
      .trim()
      .split('.')
      .reduce((acc, part) => acc * 1000 + Number(part), 0);
    if (!best || score > best.score) best = { path: candidate, cap, score };
  }
}

if (!best) {
  console.error(
    'error: these TypeScript sources run directly on Node.js >= 23.6 ' +
      '(or >= 22.6 with --experimental-strip-types); no suitable runtime was found.',
  );
  process.exit(1);
}
run(best.path, best.cap === 'flag' ? ['--experimental-strip-types'] : []);
