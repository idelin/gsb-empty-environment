/** Capture the error thrown by fn (assert.throws does not return it). */
export function captureError<E extends Error = Error>(fn: () => void): E {
  try {
    fn();
  } catch (err) {
    return err as E;
  }
  throw new Error("expected function to throw, but it returned normally");
}
