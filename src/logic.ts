import { Tri } from './result';

export const andTable: Record<Tri, Record<Tri, Tri>> = {
  true: { true: 'true', false: 'false', unknown: 'unknown' },
  false: { true: 'false', false: 'false', unknown: 'false' },
  unknown: { true: 'unknown', false: 'false', unknown: 'unknown' },
};

export const orTable: Record<Tri, Record<Tri, Tri>> = {
  true: { true: 'true', false: 'true', unknown: 'true' },
  false: { true: 'true', false: 'false', unknown: 'unknown' },
  unknown: { true: 'true', false: 'unknown', unknown: 'unknown' },
};

export const notTable: Record<Tri, Tri> = {
  true: 'false',
  false: 'true',
  unknown: 'unknown',
};
