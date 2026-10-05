// 命令行：压缩 / 解压 .lzh1 文件（多块串联），并输出大小统计。
// 用法:
//   node src/cli.ts compress   <输入> <输出> [--window N] [--block-size N]
//   node src/cli.ts decompress <输入> <输出>
//   node src/cli.ts stats      <压缩文件>
import { readFileSync, writeFileSync } from 'node:fs';
import { basename } from 'node:path';
import {
  BlockDecoder,
  FIXED_HEADER_SIZE,
  FormatError,
  MAX_WINDOW,
  decodeBlock,
  encodeBlock,
  parseHeader,
} from './index.ts';

const DEFAULT_BLOCK_SIZE = 256 * 1024;

function fail(message: string): never {
  console.error(`错误: ${message}`);
  process.exit(2);
}

function parseIntArg(args: string[], name: string, fallback: number): number {
  const idx = args.indexOf(name);
  if (idx === -1) return fallback;
  const value = Number(args[idx + 1]);
  if (!Number.isInteger(value) || value <= 0) fail(`${name} 需要正整数`);
  return value;
}

function compress(inputPath: string, outputPath: string, window: number, blockSize: number): void {
  const data = readFileSync(inputPath);
  const chunks: Uint8Array[] = [];
  let compressed = 0;
  let blocks = 0;
  for (let off = 0; off < data.length || (off === 0 && data.length === 0); off += blockSize) {
    const slice = data.subarray(off, off + blockSize);
    const block = encodeBlock(slice, window);
    chunks.push(block);
    compressed += block.length;
    blocks++;
    if (data.length === 0) break;
  }
  writeFileSync(outputPath, Buffer.concat(chunks.map((c) => Buffer.from(c))));
  printStats(data.length, compressed, blocks);
}

function decompress(inputPath: string, outputPath: string): void {
  const data = readFileSync(inputPath);
  const parts: Uint8Array[] = [];
  let off = 0;
  let blocks = 0;
  let original = 0;
  while (off < data.length) {
    if (data.length - off < FIXED_HEADER_SIZE) {
      fail(`偏移 ${off}: 尾部不足一个块头，文件截断或损坏`);
    }
    let info;
    try {
      info = parseHeader(data.subarray(off));
    } catch (err) {
      fail(`偏移 ${off}: ${(err as Error).message}`);
    }
    if (off + info!.totalBytes > data.length) {
      fail(`偏移 ${off}: 块载荷截断`);
    }
    let decoded;
    try {
      decoded = decodeBlock(data.subarray(off, off + info!.totalBytes));
    } catch (err) {
      fail(`偏移 ${off}: ${(err as Error).message}`);
    }
    parts.push(decoded);
    original += decoded.length;
    off += info!.totalBytes;
    blocks++;
  }
  writeFileSync(outputPath, Buffer.concat(parts.map((p) => Buffer.from(p))));
  printStats(original, data.length, blocks);
}

function stats(inputPath: string): void {
  const data = readFileSync(inputPath);
  let off = 0;
  let blocks = 0;
  let original = 0;
  while (off < data.length) {
    const info = parseHeader(data.subarray(off));
    original += info.originalLength;
    off += info.totalBytes;
    blocks++;
  }
  printStats(original, data.length, blocks);
}

function printStats(original: number, compressed: number, blocks: number): void {
  const ratio = original > 0 ? ((compressed / original) * 100).toFixed(2) : '—';
  console.error(`块数:       ${blocks}`);
  console.error(`原始大小:   ${original} 字节`);
  console.error(`压缩大小:   ${compressed} 字节`);
  console.error(`压缩比例:   ${ratio}%`);
  console.error('注意: 大小统计仅供参考，压缩率下降不是正确性证明。');
}

function main(): void {
  const [, , cmd, input, output, ...rest] = process.argv;
  if (!cmd || !input || (cmd !== 'stats' && !output)) {
    console.error(
      `用法:\n` +
        `  node ${basename(process.argv[1])} compress <输入> <输出> [--window N] [--block-size N]\n` +
        `  node ${basename(process.argv[1])} decompress <输入> <输出>\n` +
        `  node ${basename(process.argv[1])} stats <压缩文件>`,
    );
    process.exit(2);
  }
  try {
    if (cmd === 'compress') {
      const window = parseIntArg(rest, '--window', MAX_WINDOW);
      const blockSize = parseIntArg(rest, '--block-size', DEFAULT_BLOCK_SIZE);
      if (window > MAX_WINDOW) fail(`--window 最大 ${MAX_WINDOW}`);
      compress(input, output, window, blockSize);
    } else if (cmd === 'decompress') {
      decompress(input, output);
    } else if (cmd === 'stats') {
      stats(input);
    } else {
      fail(`未知命令: ${cmd}`);
    }
  } catch (err) {
    if (err instanceof FormatError) fail(err.message);
    throw err;
  }
}

main();
