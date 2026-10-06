import { lit, list, match, substitute, tail, v } from '../src';
import { check, checkEqual, checkThrows, section } from './harness';

export function runTargeted(): void {
  section('targeted: literals and single-node variables');

  const r1 = match(lit('a'), 'a');
  check(r1.ok, 'literal matches itself');
  const r2 = match(lit('a'), 'b');
  check(!r2.ok && r2.path.length === 0, 'literal mismatch fails at root path');

  const r3 = match(v('x'), ['a', 'b']);
  check(r3.ok && r3.bindings.length === 1, 'variable binds a whole list node');
  if (r3.ok) checkEqual(r3.bindings[0].value, ['a', 'b'], 'variable value is the list');

  section('targeted: repeated variables must bind equal structures');

  const r4 = match(list(v('x'), v('x')), ['a', 'a']);
  check(r4.ok, '[?x ?x] matches [a a]');
  const r5 = match(list(v('x'), v('x')), ['a', 'b']);
  check(!r5.ok, '[?x ?x] rejects [a b]');
  if (!r5.ok) checkEqual(r5.path, [1], 'conflict reported at the later occurrence');

  const r6 = match(list(list(v('x')), list(v('x'))), [['a'], ['a']]);
  check(r6.ok, 'nested [[?x] [?x]] matches [[a] [a]]');
  const r7 = match(list(list(v('x')), list(v('x'))), [['a'], ['b']]);
  check(!r7.ok, 'nested [[?x] [?x]] rejects [[a] [b]]');
  if (!r7.ok) checkEqual(r7.path, [1, 0], 'nested conflict path is [1 0]');

  const r8 = match(list(v('x'), list(v('x'))), [['b'], [['b']]]);
  check(r8.ok, 'variable may bind a list and recur inside a list');
  const r8b = match(list(v('x'), list(v('x'))), [['b'], ['b']]);
  check(!r8b.ok, 'list-bound variable rejects a shallower second occurrence');

  section('targeted: tail variables, empty tail, nesting');

  const r9 = match(list(lit('a'), tail('rest')), ['a']);
  check(r9.ok, 'empty tail is legal');
  if (r9.ok) checkEqual(r9.bindings, [{ name: 'rest', value: [] }], 'empty tail binds []');

  const r10 = match(list(tail('xs')), []);
  check(r10.ok, 'lone tail matches the empty list');
  if (r10.ok) checkEqual(r10.bindings[0].value, [], 'lone tail on [] binds []');

  const r11 = match(list(lit('a'), tail('xs')), ['a', 'b', 'c']);
  check(r11.ok, 'tail binds the remaining slice');
  if (r11.ok) checkEqual(r11.bindings[0].value, ['b', 'c'], 'slice is [b c]');

  const r12 = match(list(list(tail('xs')), tail('ys')), [['a', 'b'], 'c']);
  check(r12.ok, 'nested tails inside and outside a sublist');
  if (r12.ok) {
    checkEqual(r12.bindings[0].value, ['a', 'b'], 'inner tail xs = [a b]');
    checkEqual(r12.bindings[1].value, ['c'], 'outer tail ys = [c]');
  }

  const r13 = match(list(list(tail('xs')), list(tail('xs'))), [['a'], ['a']]);
  check(r13.ok, 'repeated tail accepts equal slices');
  const r14 = match(list(list(tail('xs')), list(tail('xs'))), [['a'], ['b']]);
  check(!r14.ok, 'repeated tail rejects unequal slices');
  if (!r14.ok) checkEqual(r14.path, [1, 0], 'repeated-tail conflict path is [1 0]');

  const r15 = match(list(lit('a'), lit('b'), tail('xs')), ['a']);
  check(!r15.ok, 'too-short list fails even with a tail');
  if (!r15.ok) checkEqual(r15.path, [], 'short list reported at the list itself');

  section('targeted: shallowest failure path');

  const r16 = match(list(lit('a'), lit('b')), ['a']);
  check(!r16.ok && r16.path.length === 0, 'length mismatch stays at the list, not a child');
  const r17 = match(list(lit('a'), list(lit('b'))), ['a', ['c']]);
  check(!r17.ok, 'deep literal mismatch fails');
  if (!r17.ok) checkEqual(r17.path, [1, 0], 'deep literal mismatch path is [1 0]');
  const r18 = match(list(list(lit('a'), lit('b')), lit('c')), [['a'], 'c']);
  check(!r18.ok, 'inner length mismatch fails');
  if (!r18.ok) checkEqual(r18.path, [0], 'inner length mismatch path is [0]');
  const r19 = match(list(lit('a')), 'a');
  check(!r19.ok && r19.path.length === 0, 'atom where a list is expected fails at root');

  section('targeted: stable bindings order and determinism');

  const pattern = list(v('y'), list(v('x'), tail('xs')), lit('a'));
  const subject = ['n1', ['n2', 'n3', 'n4'], 'a'];
  const first = match(pattern, subject);
  const second = match(pattern, subject);
  check(first.ok, 'mixed pattern matches');
  if (first.ok) {
    checkEqual(
      first.bindings.map((b) => b.name),
      ['y', 'x', 'xs'],
      'bindings follow first-occurrence order',
    );
  }
  checkEqual(second, first, 'repeated matching is deterministic');

  section('targeted: substitution round-trip');

  if (first.ok) {
    checkEqual(substitute(pattern, first.bindings), subject, 'substitute rebuilds the subject');
  }
  const r20 = match(list(tail('xs'), ), ['a', 'a']);
  if (r20.ok) checkEqual(substitute(list(tail('xs')), r20.bindings), ['a', 'a'], 'tail substitution splices');

  section('targeted: pattern validation');

  checkThrows(() => list(tail('xs'), lit('a')), 'tail before the last item throws');
  checkThrows(() => list(list(tail('xs'), v('y'))), 'nested misplaced tail throws');
  checkThrows(() => list(v('x'), list(tail('x'))), 'same name as var and tail throws');
  checkThrows(() => list(v(''), lit('a')), 'empty variable name throws');
  checkThrows(() => list(lit(['a'] as never)), 'non-atom literal throws');
}
