import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CabacEncoder, CabacDecoder, createContexts, createContext } from '../src/cabac.ts';
import { BitWriter, BitReader } from '../src/bitstream.ts';
import { mulberry32 } from '../src/isa.ts';

test('引擎往返：随机 bin 流（自适应模式）', () => {
  const rand = mulberry32(7);
  const NCTX = 16;
  const bins: Array<[number, 0 | 1]> = [];
  for (let i = 0; i < 20_000; i++) {
    bins.push([Math.floor(rand() * NCTX), (rand() < 0.3 ? 1 : 0) as 0 | 1]);
  }
  const writer = new BitWriter();
  const enc = new CabacEncoder(writer, true);
  const encCtxs = createContexts(NCTX);
  for (const [c, b] of bins) enc.encodeBin(b, encCtxs[c]);
  const data = enc.finish();

  const dec = new CabacDecoder(new BitReader(data), true);
  const decCtxs = createContexts(NCTX);
  for (const [c, b] of bins) assert.equal(dec.decodeBin(decCtxs[c]), b);
});

test('引擎往返：静态冻结模式（adaptive=false）', () => {
  const rand = mulberry32(11);
  const bins: Array<[number, 0 | 1]> = [];
  for (let i = 0; i < 5_000; i++) bins.push([i % 4, (rand() < 0.5 ? 1 : 0) as 0 | 1]);
  const writer = new BitWriter();
  const enc = new CabacEncoder(writer, false);
  const encCtxs = createContexts(4);
  for (const [c, b] of bins) enc.encodeBin(b, encCtxs[c]);
  const data = enc.finish();
  const dec = new CabacDecoder(new BitReader(data), false);
  const decCtxs = createContexts(4);
  for (const [c, b] of bins) assert.equal(dec.decodeBin(decCtxs[c]), b);
});

test('概率状态随命中迁移：偏斜源压缩且状态编号爬升', () => {
  const rand = mulberry32(3);
  const writer = new BitWriter();
  const enc = new CabacEncoder(writer, true);
  const ctx = createContext();
  const total = 20_000;
  for (let i = 0; i < total; i++) enc.encodeBin(rand() < 0.95 ? 1 : 0, ctx);
  enc.finish();
  const bitsPerBin = writer.bitsWritten / total;
  // H(0.95) ≈ 0.286 bit，允许有限状态机与有限长损耗
  assert.ok(bitsPerBin < 0.35, `bits/bin=${bitsPerBin} 应 < 0.35`);
  assert.ok(ctx.state > 50, `状态编号应爬升至高概率区，实际 ${ctx.state}`);
  assert.equal(ctx.mps, 1);
});

test('MPS 翻转：符号方向切换后模型自适应跟踪', () => {
  const writer = new BitWriter();
  const enc = new CabacEncoder(writer, true);
  const ctx = createContext();
  for (let i = 0; i < 2_000; i++) enc.encodeBin(1, ctx); // 先学 1 为 MPS
  assert.equal(ctx.mps, 1);
  for (let i = 0; i < 4_000; i++) enc.encodeBin(0, ctx); // 再切到 0
  assert.equal(ctx.mps, 0, 'MPS 应翻转为 0');
  assert.ok(ctx.state > 50);
});

test('静态均匀模型：每 bin 约 1 bit（不压缩）', () => {
  const rand = mulberry32(5);
  const writer = new BitWriter();
  const enc = new CabacEncoder(writer, false);
  const ctx = createContext(); // state 0 => p = 0.5
  const total = 10_000;
  for (let i = 0; i < total; i++) enc.encodeBin(rand() < 0.9 ? 1 : 0, ctx);
  enc.finish();
  const bitsPerBin = writer.bitsWritten / total;
  assert.ok(bitsPerBin > 0.99 && bitsPerBin < 1.01, `bits/bin=${bitsPerBin} 应 ≈ 1`);
});

test('穷举往返：所有长度 ≤ 10 的 bin 序列（多上下文配置）', () => {
  for (let len = 1; len <= 10; len++) {
    for (let mask = 0; mask < (1 << len); mask++) {
      const bins = Array.from({ length: len }, (_, i) => ((mask >> i) & 1) as 0 | 1);
      for (const nctx of [1, 3]) {
        const writer = new BitWriter();
        const enc = new CabacEncoder(writer, true);
        const encCtxs = createContexts(nctx);
        bins.forEach((b, i) => enc.encodeBin(b, encCtxs[i % nctx]));
        const data = enc.finish();
        const dec = new CabacDecoder(new BitReader(data), true);
        const decCtxs = createContexts(nctx);
        for (let i = 0; i < len; i++) {
          assert.equal(dec.decodeBin(decCtxs[i % nctx]), bins[i],
            `len=${len} mask=${mask} nctx=${nctx} 第 ${i} 位失配`);
        }
      }
    }
  }
});
