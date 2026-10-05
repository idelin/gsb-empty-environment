#!/usr/bin/env node
/**
 * Continuous collision detection for axis-aligned rectangles.
 *
 * Usage:
 *   node src/cli.ts scenario.json
 *   node src/cli.ts - < scenario.json
 *
 * Scenario JSON:
 * {
 *   "T": "10",                     // query interval [0, T], rational
 *   "cellSize": "2",               // spatial hash cell size, rational > 0
 *   "batches": [                   // object updates take effect per batch
 *     {
 *       "add": [
 *         { "id": "a",
 *           "rect": { "x": "0", "y": "0", "w": "1", "h": "1" },
 *           "v": ["1/2", "0"] }
 *       ],
 *       "remove": ["old-id"],
 *       "update": [{ "id": "b", "v": ["0", "-3/4"] }]
 *     }
 *   ]
 * }
 * All numbers are exact rationals: "3/2", "7", "-0.25", 1.5, ...
 */
import { readFileSync } from "node:fs";
import { World } from "./ccd.ts";
import type { BatchOp } from "./ccd.ts";

function main(argv: string[]): void {
  const args = argv.filter((a) => a !== "--no-verify");
  const verify = args.length === argv.length;
  const path = args[2] ?? "-";
  const raw = path === "-" ? readFileSync(0, "utf8") : readFileSync(path, "utf8");
  const scenario = JSON.parse(raw) as {
    T: string | number;
    cellSize?: string | number;
    batches?: BatchOp[];
  };

  const world = new World({ T: scenario.T, cellSize: scenario.cellSize });
  const batches = scenario.batches ?? [];
  const out = {
    T: world.T.toString(),
    cellSize: world.cellSize.toString(),
    batches: batches.map((op, i) => {
      world.applyBatch(op);
      const result = world.query({ verify });
      return {
        batch: i,
        collisions: result.collisions.map((c) => ({
          pair: c.pair,
          interval: [c.t0.toString(), c.t1.toString()],
        })),
        stats: result.stats,
      };
    }),
  };
  process.stdout.write(JSON.stringify(out, null, 2) + "\n");

  const failed = out.batches.filter((b) => !b.stats.verified);
  if (failed.length > 0) {
    process.stderr.write(
      `verification FAILED in batch(es) ${failed.map((b) => b.batch).join(", ")}\n`,
    );
    process.exitCode = 1;
  }
}

main(process.argv);
