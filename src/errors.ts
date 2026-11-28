/**
 * 安全 XML 组件的统一错误类型。
 * 所有防御性中止都抛出 {@link XmlSecurityError}，并携带稳定的机器可读错误码。
 */
export type XmlSecurityErrorCode =
  | 'ENCODING_MALFORMED'
  | 'ENCODING_NOT_WHITELISTED'
  | 'ENCODING_MISMATCH'
  | 'DECLARATION_MALFORMED'
  | 'INPUT_TOO_LARGE'
  | 'DTD_STRICT_MODE'
  | 'DTD_EXTERNAL_REFERENCE'
  | 'DTD_PARAMETER_ENTITY'
  | 'DTD_UNSUPPORTED_CONSTRUCT'
  | 'DTD_MALFORMED'
  | 'ENTITY_RECURSION'
  | 'ENTITY_EXPANSION_TOTAL_EXCEEDED'
  | 'ENTITY_EXPANSION_MULTIPLIER_EXCEEDED'
  | 'ENTITY_EXPANSION_DEPTH_EXCEEDED'
  | 'ENTITY_UNDECLARED'
  | 'CHARACTER_REFERENCE_INVALID'
  | 'NAMESPACE_PREFIX_UNDECLARED'
  | 'XINCLUDE_BLOCKED'
  | 'XSLT_IMPORT_BLOCKED'
  | 'XSLT_DOCUMENT_BLOCKED'
  | 'ELEMENT_DEPTH_EXCEEDED'
  | 'XML_MALFORMED';

export class XmlSecurityError extends Error {
  readonly code: XmlSecurityErrorCode;
  readonly offset?: number;

  constructor(code: XmlSecurityErrorCode, message: string, offset?: number) {
    const location = offset === undefined ? '' : ` (offset ${offset})`;
    super(`[${code}] ${message}${location}`);
    this.name = 'XmlSecurityError';
    this.code = code;
    if (offset !== undefined) {
      this.offset = offset;
    }
  }
}
