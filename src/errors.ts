import type { HandshakeErrorCode } from './protocol.ts';

export class WorkerProxyError extends Error {
  readonly code: HandshakeErrorCode | 'factory_error' | 'no_port';

  constructor(
    message: string,
    code: HandshakeErrorCode | 'factory_error' | 'no_port',
  ) {
    super(message);
    this.name = 'WorkerProxyError';
    this.code = code;
  }
}

export function handshakeError(
  code: HandshakeErrorCode,
  message: string,
): WorkerProxyError {
  return new WorkerProxyError(message, code);
}
