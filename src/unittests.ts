// Targeted tests for the two legality hazards: aliasing stores and
// exception ordering.
import { optimize } from './cse';
import { fmtProgram, Instr, Program } from './lang';
import { run, sameOutcome } from './interp';

let failures = 0;

function check(name: string, cond: boolean, detail?: string): void {
  if (cond) {
    console.log(`ok   ${name}`);
  } else {
    failures++;
    console.error(`FAIL ${name}${detail ? `\n${detail}` : ''}`);
  }
}

function countKind(p: Program, kind: Instr['kind']): number {
  return p.instrs.filter((i) => i.kind === kind).length;
}

const mem = { inputs: [], memory: [10, 11, 12, 13, 14, 15, 16, 17] };

// 1. Pure-op CSE: identical add folds to one.
{
  const p: Program = { instrs: [
    { kind: 'const', dst: 0, value: 2 },
    { kind: 'const', dst: 1, value: 3 },
    { kind: 'binop', dst: 2, op: 'add', a: 0, b: 1 },
    { kind: 'binop', dst: 3, op: 'add', a: 1, b: 0 }, // commutative twin
    { kind: 'ret', value: 3 },
  ]};
  const o = optimize(p);
  check('pure binop CSE (commutative)', countKind(o.prog, 'binop') === 1 && o.eliminated === 1,
    fmtProgram(o.prog));
  check('pure binop CSE outcome', sameOutcome(run(p, mem), run(o.prog, mem)));
}

// 2. Aliasing: a store between two identical loads forbids CSE.
{
  const p: Program = { instrs: [
    { kind: 'const', dst: 0, value: 1 },   // addr
    { kind: 'const', dst: 1, value: 2 },   // other addr (may alias at runtime)
    { kind: 'const', dst: 2, value: 99 },  // value
    { kind: 'load', dst: 3, addr: 0 },
    { kind: 'store', addr: 1, value: 2 },  // may alias t0
    { kind: 'load', dst: 4, addr: 0 },     // must NOT reuse t3
    { kind: 'ret', value: 4 },
  ]};
  const o = optimize(p);
  check('store kills load CSE', countKind(o.prog, 'load') === 2, fmtProgram(o.prog));
  // Concrete aliasing witness: both addresses are 1.
  const pAlias: Program = { instrs: [
    { kind: 'const', dst: 0, value: 1 },
    { kind: 'const', dst: 1, value: 1 },
    { kind: 'const', dst: 2, value: 99 },
    { kind: 'load', dst: 3, addr: 0 },
    { kind: 'store', addr: 1, value: 2 },
    { kind: 'load', dst: 4, addr: 0 },
    { kind: 'ret', value: 4 },
  ]};
  const oAlias = optimize(pAlias);
  const a = run(pAlias, mem);
  const b = run(oAlias.prog, mem);
  check('aliasing witness returns 99', a.ret === 99 && sameOutcome(a, b),
    `ret=${a.ret} vs ${b.ret}`);
}

// 3. Store-to-load forwarding through the same address temp.
{
  const p: Program = { instrs: [
    { kind: 'const', dst: 0, value: 4 },
    { kind: 'const', dst: 1, value: 77 },
    { kind: 'store', addr: 0, value: 1 },
    { kind: 'load', dst: 2, addr: 0 }, // forwards to t1
    { kind: 'ret', value: 2 },
  ]};
  const o = optimize(p);
  check('store-to-load forwarding', countKind(o.prog, 'load') === 0, fmtProgram(o.prog));
  check('store-to-load outcome', sameOutcome(run(p, mem), run(o.prog, mem)));
}

// 4. Load CSE with no intervening store is allowed.
{
  const p: Program = { instrs: [
    { kind: 'const', dst: 0, value: 3 },
    { kind: 'load', dst: 1, addr: 0 },
    { kind: 'load', dst: 2, addr: 0 },
    { kind: 'binop', dst: 3, op: 'add', a: 1, b: 2 },
    { kind: 'ret', value: 3 },
  ]};
  const o = optimize(p);
  check('load CSE without store', countKind(o.prog, 'load') === 1, fmtProgram(o.prog));
  check('load CSE outcome', sameOutcome(run(p, mem), run(o.prog, mem)));
}

// 5. Exception order: the FIRST div must survive; only the duplicate goes.
//    t1 is zero, so the program throws DivByZero; the optimized program must
//    throw identically and must not drop the throwing instruction.
{
  const p: Program = { instrs: [
    { kind: 'const', dst: 0, value: 7 },
    { kind: 'const', dst: 1, value: 0 },
    { kind: 'divmod', dst: 2, op: 'div', a: 0, b: 1 },
    { kind: 'divmod', dst: 3, op: 'div', a: 0, b: 1 },
    { kind: 'ret', value: 3 },
  ]};
  const o = optimize(p);
  check('div CSE keeps earliest (throwing) div',
    countKind(o.prog, 'divmod') === 1 && o.prog.instrs.some(
      (i) => i.kind === 'divmod' && i.dst === 2), fmtProgram(o.prog));
  const a = run(p, mem);
  const b = run(o.prog, mem);
  check('div exception preserved', a.error === 'DivByZero' && sameOutcome(a, b),
    `${a.error} vs ${b.error}`);
}

// 6. Exception order across a store: duplicate div after a store still folds
//    (the twin precedes the store, so if it did not throw the copy cannot),
//    and a throwing store still fires first.
{
  const p: Program = { instrs: [
    { kind: 'const', dst: 0, value: 8 },
    { kind: 'const', dst: 1, value: 2 },
    { kind: 'divmod', dst: 2, op: 'div', a: 0, b: 1 }, // 4, no throw
    { kind: 'const', dst: 3, value: 100 },             // OOB address
    { kind: 'store', addr: 3, value: 2 },              // throws StoreOutOfBounds
    { kind: 'divmod', dst: 4, op: 'div', a: 0, b: 1 }, // duplicate, removed
    { kind: 'ret', value: 4 },
  ]};
  const o = optimize(p);
  const a = run(p, mem);
  const b = run(o.prog, mem);
  check('div dup across store folds', countKind(o.prog, 'divmod') === 1, fmtProgram(o.prog));
  check('store exception still first', a.error === 'StoreOutOfBounds' && sameOutcome(a, b),
    `${a.error} vs ${b.error}`);
}

// 7. Load duplicate after a throwing store: load must survive (store kills
//    facts) and the store exception stays the observable one.
{
  const p: Program = { instrs: [
    { kind: 'const', dst: 0, value: 2 },
    { kind: 'load', dst: 1, addr: 0 },
    { kind: 'const', dst: 2, value: -5 }, // OOB
    { kind: 'store', addr: 2, value: 1 }, // throws
    { kind: 'load', dst: 3, addr: 0 },
    { kind: 'ret', value: 3 },
  ]};
  const o = optimize(p);
  const a = run(p, mem);
  const b = run(o.prog, mem);
  check('load after store not folded', countKind(o.prog, 'load') === 2, fmtProgram(o.prog));
  check('first exception unchanged', a.error === 'StoreOutOfBounds' && sameOutcome(a, b));
}

// 8. Write trace preserved: stores are never eliminated or reordered.
{
  const p: Program = { instrs: [
    { kind: 'const', dst: 0, value: 0 },
    { kind: 'const', dst: 1, value: 5 },
    { kind: 'store', addr: 0, value: 1 },
    { kind: 'const', dst: 2, value: 6 },
    { kind: 'store', addr: 0, value: 2 },
    { kind: 'load', dst: 3, addr: 0 }, // forwards to t2
    { kind: 'ret', value: 3 },
  ]};
  const o = optimize(p);
  const a = run(p, mem);
  const b = run(o.prog, mem);
  check('write trace preserved', sameOutcome(a, b) && a.writes.length === 2 &&
    a.writes[0]!.value === 5 && a.writes[1]!.value === 6,
    JSON.stringify(a.writes));
}

if (failures > 0) {
  console.error(`${failures} unit test(s) failed`);
  process.exit(1);
}
console.log('all unit tests passed');
