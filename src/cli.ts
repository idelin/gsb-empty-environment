import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

import {
  CATEGORY_LABELS,
  CATEGORY_ORDER,
  compareSample,
  type ChangeCategory,
  type SampleComparison,
} from './compare.ts';
import { ValidationCache } from './cache.ts';
import { validate, type ValidationError } from './validator.ts';

function main(argv: string[]): number {
  const [oldSchemaPath, newSchemaPath, samplesDir] = argv;
  if (!oldSchemaPath || !newSchemaPath || !samplesDir) {
    console.error('usage: node src/cli.ts <old-schema.json> <new-schema.json> <samples-dir>');
    return 2;
  }

  const oldSchema = JSON.parse(readFileSync(oldSchemaPath, 'utf8'));
  const newSchema = JSON.parse(readFileSync(newSchemaPath, 'utf8'));
  const sampleFiles = readdirSync(samplesDir)
    .filter((file) => file.endsWith('.json'))
    .sort();

  const cache = new ValidationCache((schema, sample) => validate(schema, sample));

  const comparisons: SampleComparison[] = sampleFiles.map((file) => {
    const sample = JSON.parse(readFileSync(join(samplesDir, file), 'utf8'));
    const oldRun = cache.validate(oldSchema, sample);
    const newRun = cache.validate(newSchema, sample);
    return compareSample(file, oldRun.result, newRun.result);
  });

  // 报告阶段再次经过缓存：键同时绑定 schema 与样本摘要，应全部命中。
  for (const file of sampleFiles) {
    const sample = JSON.parse(readFileSync(join(samplesDir, file), 'utf8'));
    cache.validate(oldSchema, sample);
    cache.validate(newSchema, sample);
  }

  const grouped = new Map<ChangeCategory, SampleComparison[]>();
  for (const category of CATEGORY_ORDER) {
    grouped.set(category, []);
  }
  for (const comparison of comparisons) {
    grouped.get(comparison.category)?.push(comparison);
  }

  const oldKey = ValidationCache.keyFor(oldSchema, {}).schemaDigest;
  const newKey = ValidationCache.keyFor(newSchema, {}).schemaDigest;

  console.log('==================================================');
  console.log(' Schema 回归对比报告');
  console.log('==================================================');
  console.log(`旧 schema: ${oldSchemaPath}`);
  console.log(`  sha256(schema) = ${oldKey}`);
  console.log(`新 schema: ${newSchemaPath}`);
  console.log(`  sha256(schema) = ${newKey}`);
  console.log(`样本目录: ${samplesDir}（${sampleFiles.length} 个样本）`);
  console.log('');

  for (const category of CATEGORY_ORDER) {
    const items = grouped.get(category) ?? [];
    console.log(`【${CATEGORY_LABELS[category]}】 ${items.length} 个`);
    for (const item of items) {
      console.log(`  - ${item.sampleId}`);
      if (category === 'new-failure') {
        printErrors(item.newErrors, '      新规则 ');
      } else if (category === 'fixed') {
        printErrors(item.oldErrors, '      旧规则 ');
      } else if (category === 'reason-changed') {
        printErrors(item.oldErrors, '      旧规则 ');
        printErrors(item.newErrors, '      新规则 ');
      } else if (category === 'still-failing') {
        printErrors(item.newErrors, '      规则   ');
      }
    }
    console.log('');
  }

  console.log('--------------------------------------------------');
  console.log(
    `缓存键 = sha256(schema)#sha256(sample)；缓存命中 ${cache.stats.hits} 次，未命中（实际计算）${cache.stats.misses} 次`,
  );
  console.log('样本内容或 schema 内容改变都会改变键，旧通过结果不会被复用。');
  return 0;
}

function printErrors(errors: ValidationError[], prefix: string): void {
  for (const error of errors) {
    console.log(
      `${prefix}- instancePath=${error.instancePath || '/'} schemaPath=${error.schemaPath} ` +
        `[${error.keyword}] ${error.message}`,
    );
  }
}

process.exitCode = main(process.argv.slice(2));
