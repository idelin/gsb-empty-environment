export { XmlSecurityError, type XmlSecurityErrorCode } from './errors.ts';
export {
  decodeXmlInput,
  decodeUtf8,
  decodeUtf16,
  type DecodeResult,
  type WhitelistedEncoding,
} from './decode.ts';
export {
  parseDocumentType,
  type DtdMode,
  type GeneralEntity,
  type DtdParseResult,
} from './dtd.ts';
export {
  detectEntityCycles,
  SafeEntityExpander,
  DEFAULT_ENTITY_QUOTAS,
  type EntityQuotas,
} from './entity.ts';
export {
  inspectXml,
  safeInspectXml,
  type ScanOptions,
  type ScanResult,
} from './scanner.ts';
