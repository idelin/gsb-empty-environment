import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { selectDevices } from '../index.ts';
import {
  allDevices,
  customHidDevice,
  fidoKey,
  otherVendorDevice,
  storageDevice,
  vendorDevice,
  vendorDeviceAlt,
} from './fixtures.ts';

describe('selectDevices 宽过滤（broad filters）', () => {
  it('仅用 vendorId 的宽过滤器返回该厂商的非受保护设备', () => {
    const result = selectDevices(allDevices, {
      filters: [{ vendorId: 0x1234 }],
    });
    assert.deepEqual(result, [vendorDevice, vendorDeviceAlt]);
  });

  it('仅用 classCode 的宽过滤器命中同类设备', () => {
    const result = selectDevices(allDevices, {
      filters: [{ classCode: 0x03 }],
    });
    assert.deepEqual(result, [vendorDevice, vendorDeviceAlt, otherVendorDevice]);
  });

  it('同一过滤器内多字段按“与”组合', () => {
    const result = selectDevices(allDevices, {
      filters: [{ vendorId: 0x1234, productId: 0x0002 }],
    });
    assert.deepEqual(result, [vendorDeviceAlt]);
  });

  it('多个过滤器之间按“或”组合', () => {
    const result = selectDevices(allDevices, {
      filters: [
        { vendorId: 0x1234, productId: 0x0001 },
        { usagePage: 0xff00 },
      ],
    });
    assert.deepEqual(result, [vendorDevice, customHidDevice]);
  });

  it('usagePage 可匹配集合中声明的用法页', () => {
    const result = selectDevices(allDevices, {
      filters: [{ usagePage: 0xff00 }],
    });
    assert.deepEqual(result, [customHidDevice]);
  });

  it('保持候选设备的原始顺序', () => {
    const result = selectDevices([customHidDevice, vendorDevice, vendorDeviceAlt], {
      filters: [{ vendorId: 0x1234 }, { usagePage: 0xff00 }],
    });
    assert.deepEqual(result, [customHidDevice, vendorDevice, vendorDeviceAlt]);
  });

  it('无设备匹配时返回空数组', () => {
    const result = selectDevices(allDevices, {
      filters: [{ vendorId: 0x0000 }],
    });
    assert.deepEqual(result, []);
  });
});

describe('selectDevices 排除过滤（exclusionFilters）', () => {
  it('从宽过滤结果中剔除命中排除过滤器的设备', () => {
    const result = selectDevices(allDevices, {
      filters: [{ vendorId: 0x1234 }],
      exclusionFilters: [{ productId: 0x0002 }],
    });
    assert.deepEqual(result, [vendorDevice]);
  });

  it('多个排除过滤器按“或”生效，命中任一即剔除', () => {
    const result = selectDevices(allDevices, {
      filters: [{ classCode: 0x03 }],
      exclusionFilters: [
        { vendorId: 0x1234, productId: 0x0001 },
        { vendorId: 0x5678 },
      ],
    });
    assert.deepEqual(result, [vendorDeviceAlt]);
  });

  it('空 exclusionFilters 数组等价于不排除', () => {
    const result = selectDevices(allDevices, {
      filters: [{ vendorId: 0x1234 }],
      exclusionFilters: [],
    });
    assert.deepEqual(result, [vendorDevice, vendorDeviceAlt]);
  });

  it('排除过滤器不能让受保护设备重新出现', () => {
    const result = selectDevices([fidoKey, storageDevice, vendorDevice], {
      filters: [{ vendorId: 0x1234 }],
      exclusionFilters: [{ productId: 0x0001 }],
    });
    assert.deepEqual(result, []);
  });
});
