import {
  buildSafeRequestOptions,
  selectDevices,
  type DeviceLike,
} from './index.ts';

const grantedDevices: DeviceLike[] = [
  { vendorId: 0x1234, productId: 0x0001, classCode: 0x03 },
  { vendorId: 0x1234, productId: 0x0002, classCode: 0x03 },
  {
    vendorId: 0x1234,
    productId: 0xbeef,
    collections: [{ usagePage: 0xf1d0 }],
  },
  { vendorId: 0x1234, interfaces: [{ classCode: 0x08 }] },
];

// 宽 vendorId 过滤 + 排除特定 productId；FIDO 密钥与存储接口自动剔除。
const picked = selectDevices(grantedDevices, {
  filters: [{ vendorId: 0x1234 }],
  exclusionFilters: [{ productId: 0x0002 }],
});
console.log(picked);
// => [{ vendorId: 0x1234, productId: 0x0001, classCode: 0x03 }]

// 构造传给 navigator.hid.requestDevice 的安全参数。
const safeOptions = buildSafeRequestOptions({
  filters: [{ vendorId: 0x1234, classCode: 0x03 }],
});
console.log(safeOptions);

// 以下调用都会抛错：
// selectDevices(grantedDevices, { filters: [] });                 // 空过滤器数组
// selectDevices(grantedDevices, { filters: [{}] });               // 宽松空过滤器
// buildSafeRequestOptions({ filters: [{ usagePage: 0xf1d0 }] }); // 显式索取 FIDO
