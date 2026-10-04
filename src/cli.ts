// Command line interface: reads a scene JSON, runs the CCD query, prints JSON.
//
// Scene format:
// {
//   "T": "3",                       query interval [0, T], required
//   "cellSize": "2",                optional, overrides the heuristic
//   "objects": [ {id, x, y, w, h, vx, vy}, ... ],
//   "updates": [                    optional batches applied in order
//     { "upsert": [...], "remove": ["id1", ...] }
//   ]
// }
// Numbers may be integers or exact "p/q" rational strings.

import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { Fraction } from './fraction.js';
import { World } from './world.js';

declare const process: any;

function usage () {
  console.error('usage: ccd [scene.json] [--cell-size <rational>]   (reads stdin when no file given)');
}

export function main (argv: string[]) {
  let file: string | undefined = undefined;
  let cellSize: Fraction | undefined = undefined;
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--cell-size') {
      i++;
      if (i >= argv.length) throw new Error('--cell-size needs a value');
      cellSize = Fraction.of(argv[i]);
    } else if (arg === '--help' || arg === '-h') {
      usage();
      return 0;
    } else if (file === undefined) {
      file = arg;
    } else {
      throw new Error('unexpected argument: ' + arg);
    }
  }
  let text = '';
  if (file === undefined) {
    text = readFileSync(0, 'utf8');
  } else {
    text = readFileSync(file, 'utf8');
  }
  const scene = JSON.parse(text);
  if (scene.T === undefined) throw new Error('scene.T (query horizon) is required');
  const world = new World();
  for (const obj of scene.objects ?? []) world.upsert(obj);
  world.commit();
  for (const batch of scene.updates ?? []) {
    for (const obj of batch.upsert ?? []) world.upsert(obj);
    for (const id of batch.remove ?? []) world.remove(id);
    world.commit();
  }
  const result = world.query(scene.T, cellSize);
  const out = {
    T: result.T.toString(),
    bodies: result.bodies,
    candidatePairs: result.candidatePairs,
    exactPairs: result.exactPairs,
    verified: result.verified,
    missedPairs: result.missedPairs,
    collisions: result.collisions.map(c => ({
      pair: [c.a, c.b],
      interval: [c.start.toString(), c.end.toString()]
    }))
  };
  console.log(JSON.stringify(out, null, 2));
  return 0;
}

if (typeof process !== 'undefined' && process.argv[1]
    && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    process.exitCode = main(process.argv.slice(2));
  } catch (err) {
    let msg = String(err);
    if (err instanceof Error) msg = err.message;
    console.error('error: ' + msg);
    process.exitCode = 1;
  }
}
