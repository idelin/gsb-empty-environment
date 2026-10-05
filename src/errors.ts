export class LedgerError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

export class ValidationError extends LedgerError {}

export class UnknownBenefitTypeError extends LedgerError {}

export class UnknownPackageError extends LedgerError {}

export class UnknownConsumptionError extends LedgerError {}

export class AlreadyRevokedError extends LedgerError {}

export class InsufficientQuotaError extends LedgerError {
  readonly benefitType: string;
  readonly requested: number;
  readonly available: number;

  constructor(benefitType: string, requested: number, available: number) {
    super(
      `额度不足：权益 ${benefitType} 请求扣减 ${requested} 次，当前可用 ${available} 次`,
    );
    this.benefitType = benefitType;
    this.requested = requested;
    this.available = available;
  }
}
