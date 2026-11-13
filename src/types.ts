/**
 * 校验包使用的中立数据模型。
 *
 * 这些结构与具体 XML/MP4 解析实现解耦：播放器既可以直接传入
 * `parseMpd()` / `parseInitSegment()` 的结果，也可以把自身已有的
 * 清单/分段解析结果适配成相同形状后调用 `validateManifest()`。
 */

/** 规范化后的 16 字节 KID：32 个小写十六进制字符，无连字符。 */
export type NormalizedKid = string;

/** 内容保护方案（CENC scheme），例如 `cenc` / `cbcs` / `cbc1` / `cens`。 */
export type SchemeType = string;

/**
 * MPD 中的 `<ContentProtection>` 元素（可能挂在 AdaptationSet 或
 * Representation 上）。`cenc:default_KID` 形如
 * `urn:uuid:01234567-89ab-cdef-0123-456789abcdef`，可同时声明多个。
 */
export interface ContentProtection {
  /** `schemeIdUri`，例如 `urn:uuid:1077efec-c022-4bdf-9047-cc25718c72fe`。 */
  schemeIdUri?: string;
  /** `value` 属性（部分清单用于标识方案，不参与本次方案一致性比较）。 */
  value?: string;
  /** `cenc:default_KID` 原始值，可为 UUID 列表（空格分隔）。 */
  defaultKids: string[];
  /** 许可证地址，来源可为 `cenc:pssh` 之外的扩展属性或厂商私有节点。 */
  licenseUrl?: string;
  /** 内联 `cenc:pssh` 的 Base64 内容（MPD 内嵌 PSSH，零段或多段）。 */
  psshBoxesBase64: string[];
  /** 原始元素定位信息，用于错误报告。 */
  locator?: string;
}

/** MPD 中的 `<Representation>`。 */
export interface Representation {
  id: string;
  /** 该 Representation 自身声明的 ContentProtection（可为空，继承自 AdaptationSet）。 */
  contentProtections: ContentProtection[];
  /** 对应的初始化分段字节（可延迟到校验时通过 fetch 得到后再传入）。 */
  initSegment?: Uint8Array;
  locator?: string;
}

/** MPD 中的 `<AdaptationSet>`。 */
export interface AdaptationSet {
  id?: string;
  contentType?: string;
  /** AdaptationSet 级别的 ContentProtection，供其下 Representation 继承。 */
  contentProtections: ContentProtection[];
  representations: Representation[];
  locator?: string;
}

/** 解析后的 MPD 模型。 */
export interface ParsedMpd {
  adaptationSets: AdaptationSet[];
}

/** 单个加密轨道（tenc/sinf）的解析结果。 */
export interface TrackProtection {
  /** 来自 `tenc` 的 16 字节 KID。 */
  kidBytes: Uint8Array;
  /** 规范化 KID（小写无连字符）。 */
  kid: NormalizedKid;
  /** `schm/schemeType`，例如 `cenc`；缺少 schm 时为 undefined。 */
  schemeType?: SchemeType;
  /** `isProtected` 标志（tenc 首字节的最高位）。 */
  isProtected: boolean;
  /** 默认加密块大小（per_sample_iv_size），仅作信息暴露。 */
  perSampleIvSize: number;
  locator: string;
}

/** 初始化分段中解析出的 PSSH 盒子。 */
export interface PsshBox {
  /** SystemID 的 16 字节十六进制（小写无连字符）。 */
  systemId: string;
  version: number;
  /** v1 盒子中的 KID 列表（规范化形式）。 */
  kids: NormalizedKid[];
  /** 完整原始盒子字节，供厂商私有解析（如 Widevine/PlayReady）使用。 */
  raw: Uint8Array;
  /** 盒子数据起始偏移（box header 之后），便于二次解析 data。 */
  dataOffset: number;
  locator: string;
}

/** 初始化分段解析结果。 */
export interface ParsedInitSegment {
  tracks: TrackProtection[];
  psshBoxes: PsshBox[];
}

/** 校验问题的错误码。 */
export type ValidationCode =
  | 'ADAPTATION_SET_EMPTY'
  | 'NO_CONTENT_PROTECTION'
  | 'DEFAULT_KID_MISSING'
  | 'DEFAULT_KID_MISMATCH'
  | 'DEFAULT_KID_MALFORMED'
  | 'INIT_SEGMENT_MISSING'
  | 'TENC_MISSING'
  | 'TENC_KID_MISMATCH'
  | 'SCHEME_MISSING'
  | 'SCHEME_MISMATCH'
  | 'PSSH_MISSING'
  | 'PSSH_KID_MISSING'
  | 'LICENSE_URL_MISSING'
  | 'LICENSE_URL_NOT_HTTPS'
  | 'LICENSE_URL_NOT_ALLOWLISTED'
  | 'LICENSE_URL_MALFORMED';

export interface ValidationIssue {
  code: ValidationCode;
  message: string;
  /** 触发问题的 AdaptationSet / Representation / 盒子定位。 */
  locator?: string;
  /** 涉及的 KID（规范化形式），便于日志关联。 */
  kid?: NormalizedKid;
}

export interface ValidationOptions {
  /**
   * 允许的 licenseUrl 白名单。
   * - 精确匹配：`https://license.example.com/v1/license`
   * - 子域通配：`https://*.example.com`（匹配任意层级子域，不含裸域）
   * 路径/查询必须与白名单条目前缀一致或完全相等。
   */
  allowedLicenseUrls: string[];
  /**
   * 期望的方案值。缺省时以 AdaptationSet 中第一个可解析的方案为准，
   * 并要求所有 Representation / tenc 与之一致。
   */
  expectedScheme?: SchemeType;
  /** 是否强制要求 MPD 内嵌 PSSH 也包含该 KID（默认 false，仅校验分段 PSSH）。 */
  requireMpdPsshKid?: boolean;
  /** 是否允许非默认端口的 https licenseUrl（默认 false）。 */
  allowNonDefaultPort?: boolean;
}

export interface ValidationResult {
  ok: boolean;
  issues: ValidationIssue[];
}
