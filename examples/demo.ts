// Runnable sample: containment between policy scopes over explicit finite
// domains. Run with: npm run demo
import {
  and, contains, diff, formatCond, formatPoint, ge, inSet, le, not, or, TRUE,
  domainOrder, type Cond, type Domain,
} from '../src/index.ts';

function show(title: string, container: Cond, containee: Cond, domain: Domain, budget?: number): void {
  const result = contains(container, containee, domain, budget === undefined ? {} : { budget });
  console.log(`\n# ${title}`);
  console.log(`  Q: does [${formatCond(container)}] contain [${formatCond(containee)}]?`);
  switch (result.status) {
    case 'contained':
      console.log('  A: contained — every point of the containee is covered.');
      break;
    case 'not-contained':
      console.log(`  A: NOT contained — minimal counterexample ${formatPoint(result.counterexample, domainOrder(domain))}`);
      break;
    case 'undecided':
      console.log(`  A: undecided (${result.reason}: ${result.detail})`);
      break;
  }
}

const ints: Domain = { x: { kind: 'int', lo: 0, hi: 10 } };

show('interval vs interval', ge('x', 3), ge('x', 2), ints);
show('reverse direction', ge('x', 2), ge('x', 3), ints);
show('empty range is contained in anything', ge('x', 0), and(ge('x', 5), le('x', 3)), ints);
show('boolean combination', or(ge('x', 7), le('x', 1)), or(le('x', 2), ge('x', 6)), ints);
show('negation', not(ge('x', 8)), le('x', 5), ints);

const twoVars: Domain = { x: { kind: 'int', lo: 0, hi: 10 }, y: { kind: 'int', lo: 0, hi: 10 } };
show('difference constraint', diff('x', '<=', 'y', 3), diff('x', '<=', 'y', 5), twoVars);
show('difference constraint (contained)', diff('x', '<=', 'y', 5), and(diff('x', '<=', 'y', 4), ge('y', 1)), twoVars);

const roles: Domain = { role: { kind: 'enum', values: ['admin', 'editor', 'viewer'] } };
show('finite set predicate', inSet('role', ['admin']), inSet('role', ['admin', 'editor']), roles);

const optional: Domain = { x: { kind: 'int', lo: 0, hi: 5, optional: true } };
show('three-valued: attribute may be unknown', ge('x', 1), TRUE, optional);

show('unknown attribute domain cannot be proven', ge('y', 3), ge('y', 2), ints);

const wide: Domain = { x: { kind: 'int', lo: 0, hi: 10_000 } };
show('budget interruption', ge('x', 5000), ge('x', 4000), wide, 3);
show('same query, generous budget', ge('x', 5000), ge('x', 4000), wide);
