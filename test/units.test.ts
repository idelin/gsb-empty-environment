import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { UnitMigrationError, UnitRegistry, formatRational } from '../src/index.ts';

const temperatureRegistry = () =>
  new UnitRegistry(
    [
      { name: 'K', dimension: 'temperature' },
      { name: 'C', dimension: 'temperature' },
      { name: 'F', dimension: 'temperature' },
    ],
    [
      { from: 'C', to: 'K', ratio: 1, offset: '273.15' },
      { from: 'F', to: 'C', ratio: '5/9', offset: '-160/9' },
    ],
  );

describe('absolute vs delta quantities', () => {
  test('absolute temperature uses the affine offset', () => {
    const reg = temperatureRegistry();
    assert.equal(formatRational(reg.convert(20, 'C', 'K', 'absolute')), '293.15');
    assert.equal(formatRational(reg.convert(20, 'C', 'F', 'absolute')), '68');
    assert.equal(formatRational(reg.convert('32', 'F', 'C', 'absolute')), '0');
  });

  test('temperature difference only scales', () => {
    const reg = temperatureRegistry();
    assert.equal(formatRational(reg.convert(20, 'C', 'K', 'delta')), '20');
    assert.equal(formatRational(reg.convert(20, 'C', 'F', 'delta')), '36');
    assert.equal(formatRational(reg.convert(18, 'F', 'C', 'delta')), '10');
  });
});

describe('compile-time cycle checks', () => {
  const lengthUnits = [
    { name: 'm', dimension: 'length' },
    { name: 'cm', dimension: 'length' },
    { name: 'mm', dimension: 'length' },
  ];

  test('consistent redundant conversions are accepted', () => {
    const reg = new UnitRegistry(lengthUnits, [
      { from: 'm', to: 'cm', ratio: 100 },
      { from: 'cm', to: 'mm', ratio: 10 },
      { from: 'm', to: 'mm', ratio: 1000 },
    ]);
    assert.equal(formatRational(reg.convert(1, 'm', 'mm', 'absolute')), '1000');
  });

  test('contradictory conversion cycles are rejected with the cycle listed', () => {
    assert.throws(
      () =>
        new UnitRegistry(lengthUnits, [
          { from: 'm', to: 'cm', ratio: 100 },
          { from: 'cm', to: 'mm', ratio: 10 },
          { from: 'm', to: 'mm', ratio: 999 },
        ]),
      (e: unknown) => {
        assert.ok(e instanceof UnitMigrationError);
        assert.equal(e.code, 'COMPILE_CYCLE');
        assert.match(e.message, /contradictory conversion cycle/);
        assert.match(e.message, /m/);
        assert.match(e.message, /mm/);
        return true;
      },
    );
  });

  test('contradictory affine offsets are caught too', () => {
    assert.throws(
      () =>
        new UnitRegistry(
          [
            { name: 'K', dimension: 'temperature' },
            { name: 'C', dimension: 'temperature' },
            { name: 'Cx', dimension: 'temperature' },
          ],
          [
            { from: 'C', to: 'K', ratio: 1, offset: '273.15' },
            { from: 'Cx', to: 'C', ratio: 1, offset: 0 },
            { from: 'Cx', to: 'K', ratio: 1, offset: '273' }, // off by 0.15
          ],
        ),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'COMPILE_CYCLE',
    );
  });
});

describe('compile-time unit checks', () => {
  test('dimension mismatch on a conversion', () => {
    assert.throws(
      () =>
        new UnitRegistry(
          [
            { name: 'm', dimension: 'length' },
            { name: 's', dimension: 'time' },
          ],
          [{ from: 'm', to: 's', ratio: 1 }],
        ),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'COMPILE_UNIT',
    );
  });

  test('unknown unit, zero ratio, disconnected unit', () => {
    assert.throws(
      () => new UnitRegistry([{ name: 'm', dimension: 'length' }], [{ from: 'm', to: 'mm', ratio: 1 }]),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'COMPILE_UNIT',
    );
    assert.throws(
      () =>
        new UnitRegistry(
          [
            { name: 'm', dimension: 'length' },
            { name: 'mm', dimension: 'length' },
          ],
          [{ from: 'm', to: 'mm', ratio: 0 }],
        ),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'COMPILE_UNIT',
    );
    assert.throws(
      () =>
        new UnitRegistry(
          [
            { name: 'm', dimension: 'length' },
            { name: 'mm', dimension: 'length' },
            { name: 'ft', dimension: 'length' },
          ],
          [{ from: 'm', to: 'mm', ratio: 1000 }],
        ),
      (e: unknown) => {
        assert.ok(e instanceof UnitMigrationError);
        assert.equal(e.code, 'COMPILE_UNIT');
        assert.match(e.message, /ft/);
        return true;
      },
    );
  });

  test('runtime conversion across dimensions is refused', () => {
    const reg = new UnitRegistry(
      [
        { name: 'm', dimension: 'length' },
        { name: 'mm', dimension: 'length' },
        { name: 's', dimension: 'time' },
      ],
      [{ from: 'm', to: 'mm', ratio: 1000 }],
    );
    assert.throws(
      () => reg.converter('m', 's'),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'NO_CONVERSION',
    );
  });
});
