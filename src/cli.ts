#!/usr/bin/env node
/**
 * 拆单求解器 CLI。
 *
 * 用法:
 *   node src/cli.ts <input.json> [--exact]
 *   cat input.json | node src/cli.ts
 *
 * 输入 JSON:
 * {
 *   "items": [{ "sku": "A", "quantity": 2 }, ...],
 *   "warehouses": [{
 *     "id": "W1",
 *     "fixedFee": 10,                  // 启用运费
 *     "perItemFee": 2,                 // 单件运费
 *     "freeShippingThreshold": 5,      // 免运门槛（可选）：满 N 件免全部运费
 *     "stock": { "A": 3, ... }
 *   }, ...],
 *   "options": { "allowOverDelivery": true }   // 默认 true；--exact 等价于 false
 * }
 *
 * 输出 JSON: 最优总费用、包裹数、最优性下界、各仓分配与超发量。
 */
import { readFileSync } from 'node:fs';
import { solve } from './solver.ts';
import type { OrderItem, SolveOptions, Warehouse } from './types.ts';

interface CliInput {
  items: OrderItem[];
  warehouses: Warehouse[];
  options?: SolveOptions;
}

function main(): void {
  const args = process.argv.slice(2);
  let file: string | null = null;
  const options: SolveOptions = {};
  for (const arg of args) {
    if (arg === '--exact') options.allowOverDelivery = false;
    else if (arg === '--help' || arg === '-h') {
      console.log('usage: node src/cli.ts <input.json> [--exact]');
      process.exit(0);
    } else file = arg;
  }

  const raw = file ? readFileSync(file, 'utf8') : readFileSync(0, 'utf8');
  const input = JSON.parse(raw) as CliInput;
  if (!Array.isArray(input.items) || !Array.isArray(input.warehouses)) {
    throw new Error('input must contain "items" and "warehouses" arrays');
  }
  const merged: SolveOptions = { ...input.options, ...options };
  const result = solve(input.items, input.warehouses, merged);
  console.log(JSON.stringify(result, null, 2));
  if (result.status !== 'optimal') process.exitCode = 1;
}

main();
