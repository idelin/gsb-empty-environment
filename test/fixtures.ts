import type { DeviceLike } from '../index.ts';

/** 普通厂商设备：vendorId 0x1234，可被宽 vendorId 过滤器命中。 */
export const vendorDevice: DeviceLike = {
  vendorId: 0x1234,
  productId: 0x0001,
  classCode: 0x03,
};

/** 同厂商另一款产品。 */
export const vendorDeviceAlt: DeviceLike = {
  vendorId: 0x1234,
  productId: 0x0002,
  classCode: 0x03,
};

/** 不同厂商的 HID 设备。 */
export const otherVendorDevice: DeviceLike = {
  vendorId: 0x5678,
  productId: 0x0001,
  classCode: 0x03,
};

/** FIDO 安全密钥：集合上声明受保护用法页 0xF1D0。 */
export const fidoKey: DeviceLike = {
  vendorId: 0x1234,
  productId: 0xbeef,
  collections: [{ usagePage: 0xf1d0, usage: 0x0001 }],
};

/** 键盘设备：顶层声明受保护用法页 0x0007。 */
export const keyboard: DeviceLike = {
  vendorId: 0x1234,
  productId: 0x0060,
  usagePage: 0x0007,
  usage: 0x0006,
};

/** 复合设备中的键盘接口：用法页出现在集合里。 */
export const compositeKeyboard: DeviceLike = {
  vendorId: 0xabcd,
  productId: 0x0001,
  collections: [
    { usagePage: 0x000c, usage: 0x0001 },
    { usagePage: 0x0007, usage: 0x0002 },
  ],
};

/** 存储接口设备：接口类代码为 0x08。 */
export const storageDevice: DeviceLike = {
  vendorId: 0x1234,
  productId: 0x1000,
  interfaces: [{ interfaceNumber: 0, classCode: 0x08 }],
};

/** 顶层类代码即存储类的 U 盘。 */
export const storageDeviceTopLevel: DeviceLike = {
  vendorId: 0x5678,
  productId: 0x2000,
  classCode: 0x08,
};

/** 自定义用法页的普通 HID 设备（非受保护）。 */
export const customHidDevice: DeviceLike = {
  vendorId: 0x9abc,
  productId: 0x0001,
  usagePage: 0xff00,
  collections: [{ usagePage: 0xff00, usage: 0x0001 }],
};

export const allDevices: DeviceLike[] = [
  vendorDevice,
  vendorDeviceAlt,
  otherVendorDevice,
  fidoKey,
  keyboard,
  compositeKeyboard,
  storageDevice,
  storageDeviceTopLevel,
  customHidDevice,
];
