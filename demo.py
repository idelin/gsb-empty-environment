'''Runnable demo for the weighted_sampler package.

Every section is labeled either [exact] (exact rational conclusion) or
[empirical] (statistical estimate, NOT a proof of unbiasedness).
Run: python3.11 demo.py
'''

from fractions import Fraction

from weighted_sampler import (
    BitSource,
    EmptySamplerError,
    SeededRandomSource,
    StaleTransactionError,
    WeightedSampler,
    sequence_probability,
)

SEED = 20261001          # recorded: all randomness below derives from this seed
EMPIRICAL_SAMPLES = 20000  # recorded: sample size of the empirical section


def section(title):
    print()
    print('== ' + title + ' ==')


def main():
    print('seed = %d, empirical sample size = %d' % (SEED, EMPIRICAL_SAMPLES))

    section('1. Exact rational weights [exact]')
    sampler = WeightedSampler(SeededRandomSource(SEED))
    weights = {'apple': Fraction(1, 2), 'banana': Fraction(1, 3),
               'cherry': Fraction(5, 6), 'date': 0}
    for key, w in weights.items():
        sampler.add(key, w)
    print('weights:', {k: str(v) for k, v in sampler.items()})
    print('total  =', sampler.total(), '(exact Fraction; date has weight 0 '
          'and can never be selected)')
    print('version =', sampler.version)

    section('2. Manual recomputation with fixed bits [exact]')
    # weights 1, 2, 3 -> N = 6, k = 3 bits; bits 101 = 5.
    # prefix sums: x -> 1, y -> 3, z -> 6; r = 5 falls in [3, 6) -> 'z'.
    manual = WeightedSampler(BitSource([1, 0, 1]))
    for key, w in [('x', 1), ('y', 2), ('z', 3)]:
        manual.add(key, w)
    print('weights x=1, y=2, z=3 -> N=6, k=3 bits; fixed bits 101 -> r=5')
    print('prefix intervals: x:[0,1) y:[1,3) z:[3,6); r=5 -> z')
    print('sample() =', manual.sample(), '(recomputable by hand)')

    section('3. Transaction rollback restores weights AND random state [exact]')
    twin = WeightedSampler(SeededRandomSource(SEED))
    for key, w in weights.items():
        twin.add(key, w)
    tx = sampler.begin()
    drawn = (tx.draw(), tx.draw())
    print('drawn inside transaction:', drawn, ' p =', tx.probability())
    tx.rollback()
    print('after rollback: total =', sampler.total(), 'version =', sampler.version)
    print('next 3 samples match untouched twin:',
          [sampler.sample() for _ in range(3)] == [twin.sample() for _ in range(3)])

    section('4. Committed without-replacement batch [exact]')
    with sampler.begin() as tx:
        batch = (tx.draw(), tx.draw())
        p = tx.probability()
    print('drawn:', batch)
    print('exact conditional probability =', p,
          '== sequence_probability:', sequence_probability(weights, batch) == p)
    print('distinct keys (no replacement):', len(set(batch)) == len(batch))
    print('total after commit =', sampler.total(), ' version =', sampler.version)

    section('5. Concurrent update invalidates the snapshot [exact]')
    tx = sampler.begin()
    tx.draw()
    sampler.update('apple', 9)  # concurrent mutation bumps the version
    try:
        tx.commit()
    except StaleTransactionError as e:
        print('commit rejected:', e)
    tx.rollback()  # transaction is still open; rollback restores random state
    print('rolled back cleanly; total =', sampler.total())

    section('6. Zero total weight fails explicitly [exact]')
    empty = WeightedSampler(SeededRandomSource(1))
    empty.add('only', 0)
    try:
        empty.sample()
    except EmptySamplerError as e:
        print('EmptySamplerError:', e)

    section('7. Frequencies vs exact probabilities [empirical]')
    print('NOTE: empirical estimate only -- the unbiasedness argument is the')
    print('exact bit-space enumeration in tests/, not these frequencies.')
    empirical = WeightedSampler(SeededRandomSource(SEED))
    live = {k: w for k, w in weights.items() if w > 0}
    for key, w in live.items():
        empirical.add(key, w)
    counts = {k: 0 for k in live}
    for _ in range(EMPIRICAL_SAMPLES):
        counts[empirical.sample()] += 1
    total_w = sum(live.values())
    for key in live:
        exact_p = live[key] / total_w
        freq = counts[key] / EMPIRICAL_SAMPLES
        print('%-7s exact=%-6s (~%.4f)  empirical=%.4f'
              % (key, exact_p, float(exact_p), freq))


if __name__ == '__main__':
    main()
