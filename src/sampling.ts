import { fingerprint } from './fingerprint.ts';
import type { SamplingCommitment } from './types.ts';

export function createSamplingCommitment(
  eventId: string,
  rate: number,
  nonce: string = fingerprint({ eventId, rate, salt: 'promise' }),
): SamplingCommitment {
  if (!Number.isFinite(rate) || rate < 0 || rate > 1) {
    throw new RangeError('sampling rate must be between 0 and 1');
  }
  const seed = Number.parseInt(fingerprint({ eventId, rate, nonce }).slice(0, 13), 16);
  const unit = seed / 0xfffffffffffff;
  const included = rate === 1 ? true : unit < rate;
  const promise = fingerprint({
    scheme: 'sample-promise-v1',
    eventId,
    rate,
    nonce,
    included,
  });
  return Object.freeze({
    scheme: 'sample-promise-v1' as const,
    rate,
    nonce,
    included,
    promise,
  });
}

export function verifySamplingCommitment(
  eventId: string,
  commitment: SamplingCommitment,
): boolean {
  if (commitment.scheme !== 'sample-promise-v1') {
    return false;
  }
  if (!Number.isFinite(commitment.rate) || commitment.rate < 0 || commitment.rate > 1) {
    return false;
  }
  const expectedPromise = fingerprint({
    scheme: 'sample-promise-v1',
    eventId,
    rate: commitment.rate,
    nonce: commitment.nonce,
    included: commitment.included,
  });
  if (expectedPromise !== commitment.promise) {
    return false;
  }
  const seed = Number.parseInt(
    fingerprint({
      eventId,
      rate: commitment.rate,
      nonce: commitment.nonce,
    }).slice(0, 13),
    16,
  );
  const unit = seed / 0xfffffffffffff;
  const expectedIncluded = commitment.rate === 1 ? true : unit < commitment.rate;
  return expectedIncluded === commitment.included;
}
