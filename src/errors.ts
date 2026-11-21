export const HandshakeErrorCode = {
  MISSING_ORIGIN: 'missing_origin',
  MALFORMED_ORIGIN: 'malformed_origin',
  UNTRUSTED_ORIGIN: 'untrusted_origin',
  CROSS_SITE_UPGRADE_REJECTED: 'cross_site_upgrade_rejected',
  MISSING_TICKET: 'missing_ticket',
  MALFORMED_TICKET: 'malformed_ticket',
  TICKET_EXPIRED: 'ticket_expired',
  TICKET_ALREADY_USED: 'ticket_already_used',
  TICKET_SCOPE_MISMATCH: 'ticket_scope_mismatch',
  TICKET_ORIGIN_MISMATCH: 'ticket_origin_mismatch',
  UNTRUSTED_PROXY_HEADERS: 'untrusted_proxy_headers',
  PROXY_CROSS_SITE_FORWARD: 'proxy_cross_site_forward',
  UNACCEPTABLE_SUBPROTOCOL: 'unacceptable_subprotocol',
  CROSS_SITE_COMPRESSION_REJECTED: 'cross_site_compression_rejected',
  UNSUPPORTED_EXTENSION: 'unsupported_extension',
} as const;

export type HandshakeErrorCode =
  (typeof HandshakeErrorCode)[keyof typeof HandshakeErrorCode];

export class HandshakeRejected extends Error {
  readonly code: HandshakeErrorCode;

  constructor(code: HandshakeErrorCode, message: string) {
    super(message);
    this.name = 'HandshakeRejected';
    this.code = code;
  }
}
