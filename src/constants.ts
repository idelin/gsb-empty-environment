/**
 * 受保护的 HID 用法页（Usage Page）。
 * 命中这些用法页的设备永远不会出现在选择结果中。
 */

/** FIDO Alliance 用法页，安全密钥（如 U2F/WebAuthn 平台认证器）。 */
export const USAGE_PAGE_FIDO = 0xf1d0;

/** Keyboard/Keypad 用法页，键盘与小键盘设备。 */
export const USAGE_PAGE_KEYBOARD = 0x0007;

/**
 * 受保护用法页集合（含 FIDO 0xF1D0 与键盘 0x07）。
 */
export const PROTECTED_USAGE_PAGES: readonly number[] = [
  USAGE_PAGE_FIDO,
  USAGE_PAGE_KEYBOARD,
];

/** USB 大容量存储（Mass Storage）接口/设备类代码。 */
export const CLASS_CODE_MASS_STORAGE = 0x08;

/** 受保护的 USB 类代码集合，存储接口设备不得列出。 */
export const PROTECTED_CLASS_CODES: readonly number[] = [
  CLASS_CODE_MASS_STORAGE,
];

/** 各过滤字段的合法取值上限（均为无符号整数）。 */
export const FIELD_MAX_VALUE = {
  vendorId: 0xffff,
  productId: 0xffff,
  usagePage: 0xffff,
  classCode: 0xff,
} as const;

/** 过滤器允许使用的全部字段名。 */
export const FILTER_FIELDS = [
  'vendorId',
  'productId',
  'classCode',
  'usagePage',
] as const;
