import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildSafeRequestOptions,
  selectDevices,
  validateFilter,
  validateFilterList,
} from '../index.ts';
import { vendorDevice } from './fixtures.ts';

describe('空过滤器拒绝', () => {
  it('拒绝空过滤器对象（宽松过滤器）', () => {
    assert.throws(() => validateFilter({}), /空过滤器/);
  });

  it('拒绝空 filters 数组', () => {
    assert.throws(
      () => selectDevices([vendorDevice], { filters: [] }),
      /不能为空数组/,
    );
  });

  it('拒绝缺失 filters', () => {
    assert.throws(
      () =>
        selectDevices([vendorDevice], {
          filters: undefined as never,
        }),
      /不能为空/,
    );
  });

  it('filters 数组中混入空对象时拒绝并标注下标', () => {
    assert.throws(
      () =>
        selectDevices([vendorDevice], {
          filters: [{ vendorId: 0x1234 }, {}],
        }),
      /filters\[1\]/,
    );
  });

  it('exclusionFilters 中的空对象同样拒绝', () => {
    assert.throws(
      () =>
        selectDevices([vendorDevice], {
          filters: [{ vendorId: 0x1234 }],
          exclusionFilters: [{}],
        }),
      /exclusionFilters\[0\]/,
    );
  });

  it('exclusionFilters 允许省略或为空数组', () => {
    assert.doesNotThrow(() =>
      validateFilterList(undefined, 'exclusionFilters', true),
    );
    assert.doesNotThrow(() =>
      validateFilterList([], 'exclusionFilters', true),
    );
  });
});

describe('字段值校验', () => {
  it('拒绝非整数与负数', () => {
    assert.throws(() => validateFilter({ vendorId: 1.5 }), /非负整数/);
    assert.throws(() => validateFilter({ productId: -1 }), /非负整数/);
    assert.throws(
      () => validateFilter({ classCode: Number.NaN }),
      /非负整数/,
    );
  });

  it('拒绝超出位宽的取值', () => {
    assert.throws(() => validateFilter({ vendorId: 0x10000 }), /超出上限/);
    assert.throws(() => validateFilter({ classCode: 0x100 }), /超出上限/);
  });

  it('接受边界值', () => {
    assert.doesNotThrow(() => validateFilter({ vendorId: 0xffff }));
    assert.doesNotThrow(() => validateFilter({ classCode: 0xff }));
  });

  it('非对象过滤器抛 TypeError', () => {
    assert.throws(() => validateFilter(null as never), /必须是对象/);
    assert.throws(
      () => validateFilterList('nope' as never, 'filters', false),
      /必须是数组/,
    );
  });

  it('buildSafeRequestOptions 同样执行空过滤器校验', () => {
    assert.throws(
      () => buildSafeRequestOptions({ filters: [{}] }),
      /filters\[0\]/,
    );
  });
});
