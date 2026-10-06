export type Tri = 'true' | 'false' | 'unknown';

export interface TypeErrorInfo {
  path: string;
  message: string;
}

export type EvalResult =
  | { ok: true; value: Tri }
  | { ok: false; error: TypeErrorInfo };

export const triOk = (value: Tri): EvalResult => ({ ok: true, value });
export const triErr = (path: string, message: string): EvalResult => ({
  ok: false,
  error: { path, message },
});

export const TRUE: EvalResult = triOk('true');
export const FALSE: EvalResult = triOk('false');
export const UNKNOWN: EvalResult = triOk('unknown');
