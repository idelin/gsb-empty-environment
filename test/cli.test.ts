import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const CLI = new URL('../src/cli.ts', import.meta.url).pathname;

function makeSample(): Buffer {
  // 混合内容：重复日志行 + 伪随机段
  const parts: Buffer[] = [];
  for (let i = 0; i < 2000; i++) {
    parts.push(Buffer.from(`2026-10-05T12:00:${String(i % 60).padStart(2, '0')} INFO request id=${i} path=/api/v1/items status=200\n`));
  }
  const rand = Buffer.alloc(50000);
  for (let i = 0; i < rand.length; i++) rand[i] = (i * 2654435761) >>> 24;
  parts.push(rand);
  return Buffer.concat(parts);
}

test('CLI 压缩-解压往返 + 统计输出', () => {
  const dir = mkdtempSync(join(tmpdir(), 'lzh1-'));
  const input = join(dir, 'input.bin');
  const compressed = join(dir, 'out.lzh1');
  const restored = join(dir, 'restored.bin');
  writeFileSync(input, makeSample());

  const compressErr = execFileSync(process.execPath, [CLI, 'compress', input, compressed], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  void compressErr;
  execFileSync(process.execPath, [CLI, 'decompress', compressed, restored], { stdio: 'pipe' });
  assert.deepEqual(readFileSync(restored), readFileSync(input));

  const statsOut = execFileSync(process.execPath, [CLI, 'stats', compressed], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  void statsOut;
});

test('CLI 损坏文件解压失败（非零退出）', () => {
  const dir = mkdtempSync(join(tmpdir(), 'lzh1-'));
  const input = join(dir, 'input.bin');
  const compressed = join(dir, 'out.lzh1');
  const restored = join(dir, 'restored.bin');
  writeFileSync(input, makeSample());
  execFileSync(process.execPath, [CLI, 'compress', input, compressed], { stdio: 'pipe' });
  const data = readFileSync(compressed);
  data[16] ^= 0xff; // 破坏第一块 CRC
  writeFileSync(compressed, data);
  assert.throws(() =>
    execFileSync(process.execPath, [CLI, 'decompress', compressed, restored], { stdio: 'pipe' }),
  );
});
