import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  analyzeLoad,
  varianceCurve,
  bitWidthCurve,
  avalancheReport,
  randomKeyGen,
  sequentialKeyGen,
} from '../src/analyzer.ts';
import { md5_32, murmur3_32, fnv1a_32 } from '../src/hashes.ts';

const NODES = Array.from({ length: 8 }, (_, i) => `node-${i}`);

test('强哈希（Murmur3）在随机键下通过卡方均匀性检验', () => {
  const r = analyzeLoad({
    hashFn: murmur3_32,
    hashName: 'murmur3',
    nodes: NODES,
    samples: 100_000,
    keyGen: randomKeyGen(42),
    vnodes: 160,
  });
  assert.ok(r.pValue > 0.001, `p=${r.pValue} 不应显著偏离均匀`);
  assert.ok(r.maxMinRatio < 1.5);
  assert.equal(r.verdict !== 'FAIL', true);
  // 计数守恒
  const total = Object.values(r.counts).reduce((a, b) => a + b, 0);
  assert.equal(total, 100_000);
});

test('退化哈希（常数）被卡方检验精准判死', () => {
  const r = analyzeLoad({
    hashFn: () => 12345,
    hashName: 'constant',
    nodes: NODES,
    samples: 10_000,
    keyGen: randomKeyGen(1),
    vnodes: 16,
  });
  assert.equal(r.verdict, 'FAIL');
  assert.equal(r.uniformPValue, 0);
  assert.ok(r.structuralCv > 1, `结构 CV=${r.structuralCv} 应暴露环退化`);
  assert.equal(r.maxMinRatio, Infinity);
});

test('方差曲线：实测 CV 与理论 CV 同量级且随 N 下降', () => {
  const rows = varianceCurve({
    hashFn: murmur3_32,
    nodes: NODES,
    sampleSizes: [1_000, 10_000, 100_000],
    keyGen: randomKeyGen(9),
    vnodes: 160,
  });
  assert.equal(rows.length, 3);
  for (const r of rows) {
    // 均匀哈希：实测 CV 应贴近环结构 CV
    assert.ok(r.cvRatio > 0.5 && r.cvRatio < 2, `cvRatio=${r.cvRatio}`);
    assert.ok(r.structuralCv > 0);
  }
  // 样本量每 ×10，采样噪声 CV 应约 ÷√10
  const ratio = rows[0].samplingCv / rows[1].samplingCv;
  assert.ok(Math.abs(ratio - Math.sqrt(10)) < 1e-9);
});

test('雪崩测试：FNV-1a 高位输出缺陷显著，Murmur3 通过', () => {
  const fnv = avalancheReport({ hashFn: fnv1a_32, samplesPerBit: 2000 });
  const mur = avalancheReport({ hashFn: murmur3_32, samplesPerBit: 2000 });
  // FNV 高位输出对输入翻转不敏感：最大偏差应显著大于 Murmur3
  assert.ok(
    fnv.overallMaxDev > mur.overallMaxDev * 3,
    `FNV maxDev=${fnv.overallMaxDev} 应远大于 Murmur3 的 ${mur.overallMaxDev}`,
  );
  assert.ok(mur.overallMaxDev < 0.1, `Murmur3 maxDev=${mur.overallMaxDev} 应 < 0.1`);
  assert.ok(fnv.deficientBits.length > 0, 'FNV 应检出缺陷输出位');
  assert.equal(mur.deficientBits.length, 0);
});

test('位宽扫描：FNV 在截断位宽下的分布缺陷重于 MD5/Murmur3', () => {
  const bitsList = [8, 16, 24, 32];
  const opts = {
    nodes: NODES,
    bitsList,
    samples: 50_000,
    keyGen: sequentialKeyGen(),
    mode: 'high' as const,
    vnodes: 160,
  };
  const fnv = bitWidthCurve({ hashFn: fnv1a_32, ...opts });
  const md5 = bitWidthCurve({ hashFn: md5_32, ...opts });
  const mur = bitWidthCurve({ hashFn: murmur3_32, ...opts });

  // 全 32 位时 FNV 取高位的均匀性应显著差于 MD5/Murmur3（顺序键）
  const fnv32 = fnv.find((r) => r.bits === 32)!;
  const md5_32row = md5.find((r) => r.bits === 32)!;
  const mur32 = mur.find((r) => r.bits === 32)!;
  assert.ok(
    fnv32.chi2 > md5_32row.chi2 && fnv32.chi2 > mur32.chi2,
    `FNV χ²=${fnv32.chi2.toFixed(1)} 应大于 MD5=${md5_32row.chi2.toFixed(1)} 与 Murmur3=${mur32.chi2.toFixed(1)}`,
  );

  // 低位宽下所有哈希都因环空间过小而劣化（碰撞数递增）
  const fnv8 = fnv.find((r) => r.bits === 8)!;
  assert.ok(fnv8.ringCollisions > 0);
  assert.equal(fnv8.verdict, 'FAIL');
});
