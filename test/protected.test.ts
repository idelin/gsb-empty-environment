import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildSafeRequestOptions,
  isProtectedDevice,
  isProtectedFilter,
  selectDevices,
  USAGE_PAGE_FIDO,
  USAGE_PAGE_KEYBOARD,
  CLASS_CODE_MASS_STORAGE,
} from '../index.ts';
import {
  allDevices,
  compositeKeyboard,
  customHidDevice,
  fidoKey,
  keyboard,
  otherVendorDevice,
  storageDevice,
  storageDeviceTopLevel,
  vendorDevice,
  vendorDeviceAlt,
} from './fixtures.ts';

describe('受保护设备不得列出', () => {
  it('识别 FIDO 0xF1D0 安全密钥', () => {
    assert.equal(isProtectedDevice(fidoKey), true);
    assert.equal(USAGE_PAGE_FIDO, 0xf1d0);
  });

  it('识别顶层与集合中的键盘用法页 0x07', () => {
    assert.equal(isProtectedDevice(keyboard), true);
    assert.equal(isProtectedDevice(compositeKeyboard), true);
    assert.equal(USAGE_PAGE_KEYBOARD, 0x0007);
  });

  it('识别接口级与设备级存储类代码 0x08', () => {
    assert.equal(isProtectedDevice(storageDevice), true);
    assert.equal(isProtectedDevice(storageDeviceTopLevel), true);
    assert.equal(CLASS_CODE_MASS_STORAGE, 0x08);
  });

  it('普通设备不受保护', () => {
    assert.equal(isProtectedDevice(vendorDevice), false);
    assert.equal(isProtectedDevice(customHidDevice), false);
  });

  it('即使宽过滤器命中，受保护设备也不会出现在结果中', () => {
    const result = selectDevices(allDevices, {
      filters: [
        { vendorId: 0x1234 },
        { vendorId: 0xabcd },
        { vendorId: 0x5678 },
      ],
    });
    assert.ok(!result.includes(fidoKey));
    assert.ok(!result.includes(keyboard));
    assert.ok(!result.includes(compositeKeyboard));
    assert.ok(!result.includes(storageDevice));
    assert.ok(!result.includes(storageDeviceTopLevel));
    assert.deepEqual(result, [
      vendorDevice,
      vendorDeviceAlt,
      otherVendorDevice,
    ]);
  });

  it('只选择受保护用法页时结果为空', () => {
    const result = selectDevices(allDevices, {
      filters: [{ usagePage: 0xf1d0 }],
    });
    assert.deepEqual(result, []);
  });
});

describe('isProtectedFilter', () => {
  it('标记指向受保护用法页/类代码的过滤器', () => {
    assert.equal(isProtectedFilter({ usagePage: 0xf1d0 }), true);
    assert.equal(isProtectedFilter({ usagePage: 0x0007 }), true);
    assert.equal(isProtectedFilter({ classCode: 0x08 }), true);
  });

  it('普通过滤器不被标记', () => {
    assert.equal(isProtectedFilter({ vendorId: 0x1234 }), false);
    assert.equal(isProtectedFilter({ usagePage: 0xff00 }), false);
  });
});

describe('buildSafeRequestOptions 拦截显式索取受保护设备', () => {
  it('filters 指向 FIDO 用法页时抛错', () => {
    assert.throws(
      () =>
        buildSafeRequestOptions({
          filters: [{ vendorId: 0x1234, usagePage: 0xf1d0 }],
        }),
      /受保护/,
    );
  });

  it('exclusionFilters 指向存储类代码时抛错', () => {
    assert.throws(
      () =>
        buildSafeRequestOptions({
          filters: [{ vendorId: 0x1234 }],
          exclusionFilters: [{ classCode: 0x08 }],
        }),
      /受保护/,
    );
  });

  it('合法过滤器返回深拷贝后的安全参数', () => {
    const input = {
      filters: [{ vendorId: 0x1234 }],
      exclusionFilters: [{ productId: 0x0002 }],
    };
    const options = buildSafeRequestOptions(input);
    assert.deepEqual(options, {
      filters: [{ vendorId: 0x1234 }],
      exclusionFilters: [{ productId: 0x0002 }],
    });
    assert.notEqual(options.filters[0], input.filters[0]);
    assert.notEqual(options.exclusionFilters[0], input.exclusionFilters[0]);
  });
});
