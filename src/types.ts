/**
 * 设备选择过滤器与设备的类型定义。
 * 字段语义对齐浏览器 HID/USB 设备请求过滤器，
 * 仅暴露宿主允许组合的四个字段。
 */

/**
 * 设备选择过滤器。所有字段可选，但至少要出现一个；
 * 字段之间为“与”关系，多个过滤器之间为“或”关系。
 */
export interface DeviceFilter {
  /** USB 厂商 ID（Vendor ID）。 */
  vendorId?: number;
  /** USB 产品 ID（Product ID）。 */
  productId?: number;
  /** USB（接口/设备）类代码（Class Code）。 */
  classCode?: number;
  /** HID 用法页（Usage Page），如 FIDO 0xF1D0。 */
  usagePage?: number;
}

/** HID 集合（collection），用法页可能出现在集合而非设备顶层。 */
export interface DeviceCollection {
  usagePage?: number;
  usage?: number;
}

/**
 * 待选择的设备描述。
 * 顶层字段用于设备级匹配；collections/interfaces 承载接口/集合级信息。
 */
export interface DeviceLike {
  vendorId?: number;
  productId?: number;
  /** 设备级类代码（部分设备类只在接口上声明）。 */
  classCode?: number;
  /** 设备级用法页。 */
  usagePage?: number;
  /** 设备级用法（Usage），仅作描述，不参与匹配。 */
  usage?: number;
  /** HID 集合列表，其中任意集合的用法页可参与用法页匹配/保护判断。 */
  collections?: DeviceCollection[];
  /** 接口列表，接口类代码用于 classCode 匹配与存储接口保护判断。 */
  interfaces?: DeviceInterface[];
}

/** 设备接口描述。 */
export interface DeviceInterface {
  interfaceNumber?: number;
  classCode?: number;
  subclassCode?: number;
  protocolCode?: number;
}

/** 设备选择入参。 */
export interface DeviceSelectionOptions {
  /** 包含过滤器：设备匹配其中任一过滤器即通过基础筛选，不得为空。 */
  filters: DeviceFilter[];
  /** 排除过滤器：命中任一过滤器的设备被排除；可省略或为空数组。 */
  exclusionFilters?: DeviceFilter[];
}

/**
 * 面向浏览器原生 requestDevice 的安全过滤器包。
 * 已通过结构校验且不含受保护设备。
 */
export interface SafeRequestOptions {
  filters: DeviceFilter[];
  exclusionFilters: DeviceFilter[];
}
