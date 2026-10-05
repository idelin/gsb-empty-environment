import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { stripSource } from './strip-types.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const buildDir = path.join(root, '.build');

const sources = [];
function walk(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full);
    else if (entry.name.endsWith('.ts')) sources.push(full);
  }
}
walk(path.join(root, 'src'));
walk(path.join(root, 'tests'));

fs.rmSync(buildDir, { recursive: true, force: true });

const outputs = [];
for (const file of sources) {
  const rel = path.relative(root, file);
  const out = path.join(buildDir, rel.replace(/\.ts$/, '.js'));
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, stripSource(fs.readFileSync(file, 'utf8')));
  outputs.push(out);
}

let checkFailed = false;
for (const file of outputs) {
  try {
    execFileSync(process.execPath, ['--check', file], { stdio: 'pipe' });
  } catch (err) {
    checkFailed = true;
    console.error(`语法检查失败: ${path.relative(root, file)}`);
    console.error(String(err.stderr || err.message));
  }
}
if (checkFailed) process.exit(1);

const testDir = path.join(buildDir, 'tests');
const testFiles = fs.readdirSync(testDir).filter((f) => f.endsWith('.test.js'));
for (const f of testFiles) {
  await import(pathToFileURL(path.join(testDir, f)).href);
}
const { runTests } = await import(pathToFileURL(path.join(testDir, 'testkit.js')).href);
const failed = runTests();
process.exit(failed === 0 ? 0 : 1);
