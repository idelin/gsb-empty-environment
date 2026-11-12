export class PnaError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = new.target.name;
    this.code = code;
  }
}

export class InvalidIpError extends PnaError {
  readonly input: unknown;

  constructor(input: unknown) {
    super('PNA_INVALID_IP', `invalid IP address: ${String(input)}`);
    this.input = input;
  }
}

export class PrivateNetworkAccessDeniedError extends PnaError {
  readonly reason:
    | 'bad-status'
    | 'missing-allow-private-network'
    | 'invalid-allow-private-network';
  readonly status: number | undefined;

  constructor(
    reason:
      | 'bad-status'
      | 'missing-allow-private-network'
      | 'invalid-allow-private-network',
    status: number | undefined,
  ) {
    super(
      'PNA_DENIED',
      `private network access denied (${reason}${status === undefined ? '' : `, status ${status}`})`,
    );
    this.reason = reason;
    this.status = status;
  }
}

export class RebindingDetectedError extends PnaError {
  readonly preflightIp: string;
  readonly actualIp: string;

  constructor(preflightIp: string, actualIp: string) {
    super(
      'PNA_REBINDING',
      `peer IP changed between preflight (${preflightIp}) and connection (${actualIp}); aborting`,
    );
    this.preflightIp = preflightIp;
    this.actualIp = actualIp;
  }
}
