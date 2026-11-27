import {
  constants as zlibConstants,
  createInflate,
  createInflateRaw,
  type Inflate,
} from "node:zlib";
import { JweSecurityError } from "./errors.ts";

export interface BoundedInflateOptions {
  /** Hard upper bound on decompressed octets. */
  readonly maxPlaintextBytes: number;
  /** Hard upper bound on decompressed/compressed byte ratio. */
  readonly maxRatio: number;
  /**
   * RFC 7516 zip="DEF" is RFC 1951 raw DEFLATE. "auto" also accepts the
   * commonly emitted RFC 1950 zlib wrapper while preserving the same limits.
   */
  readonly format?: "auto" | "raw" | "zlib";
}

interface InflateAttemptFailure {
  readonly dataError: boolean;
  readonly outputBytes: number;
  readonly cause: unknown;
}

class AttemptFailure extends Error {
  readonly dataError: boolean;
  readonly outputBytes: number;
  readonly cause: unknown;

  constructor(failure: InflateAttemptFailure) {
    super("DEFLATE stream failed");
    this.name = "AttemptFailure";
    this.dataError = failure.dataError;
    this.outputBytes = failure.outputBytes;
    this.cause = failure.cause;
  }
}

const DATA_ERROR_CODES = new Set([
  "Z_DATA_ERROR",
  "Z_NEED_DICT",
  "Z_BUF_ERROR",
]);

function assertLimit(value: unknown, name: string, allowZero: boolean): number {
  if (
    typeof value !== "number" ||
    !Number.isFinite(value) ||
    value < 0 ||
    (!allowZero && value === 0)
  ) {
    throw new JweSecurityError(
      "JWE_POLICY_INVALID",
      `${name} must be ${allowZero ? "a non-negative" : "a positive"} finite number`,
    );
  }
  return value;
}

function looksLikeZlibWrapper(input: Buffer): boolean {
  if (input.length < 2) {
    return false;
  }
  const cmf = input[0]!;
  const flg = input[1]!;
  return (
    (cmf & 0x0f) === 8 &&
    (cmf >> 4) <= 7 &&
    ((cmf << 8) + flg) % 31 === 0
  );
}

function inflateAttempt(
  factory: () => Inflate,
  input: Buffer,
  maxPlaintextBytes: number,
  maxRatio: number,
): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const ratioCap = Math.floor(input.length * maxRatio);

    if (ratioCap < 0) {
      reject(
        new JweSecurityError(
          "JWE_DECOMPRESSION_RATIO_LIMIT",
          "compressed input is too large to evaluate the ratio limit",
        ),
      );
      return;
    }

    const stream = factory();
    const chunks: Buffer[] = [];
    let total = 0;
    let settled = false;

    const settleReject = (error: Error): void => {
      if (settled) {
        return;
      }
      settled = true;
      stream.destroy();
      reject(error);
    };

    stream.on("data", (chunk: Buffer) => {
      if (settled) {
        return;
      }

      total += chunk.length;
      if (total > maxPlaintextBytes) {
        settleReject(
          new JweSecurityError(
            "JWE_DECOMPRESSION_LIMIT",
            "decompressed payload exceeds maxPlaintextBytes",
            {
              maxPlaintextBytes,
              outputBytesAtAbort: total,
            },
          ),
        );
        return;
      }
      if (total > ratioCap) {
        settleReject(
          new JweSecurityError(
            "JWE_DECOMPRESSION_RATIO_LIMIT",
            "decompressed payload exceeds maxRatio",
            {
              maxRatio,
              compressedBytes: input.length,
              ratioLimitBytes: ratioCap,
              outputBytesAtAbort: total,
            },
          ),
        );
        return;
      }

      chunks.push(chunk);
    });

    stream.on("error", (error: NodeJS.ErrnoException) => {
      if (settled) {
        return;
      }
      settled = true;
      reject(
        new AttemptFailure({
          dataError:
            typeof error.code === "string" &&
            DATA_ERROR_CODES.has(error.code),
          outputBytes: total,
          cause: error,
        }),
      );
    });

    stream.on("end", () => {
      if (settled) {
        return;
      }
      settled = true;
      resolve(Buffer.concat(chunks, total));
    });

    stream.on("close", () => {
      if (settled) {
        return;
      }
      settled = true;
      reject(
        new AttemptFailure({
          dataError: false,
          outputBytes: total,
          cause: new Error("DEFLATE stream closed without ending"),
        }),
      );
    });

    stream.end(input);
  });
}

function wrapFailure(failure: unknown): JweSecurityError {
  if (failure instanceof JweSecurityError) {
    return failure;
  }
  if (failure instanceof AttemptFailure) {
    return new JweSecurityError(
      "JWE_DECOMPRESSION_FAILED",
      "compressed payload is not valid DEFLATE data",
      {
        dataError: failure.dataError,
        outputBytes: failure.outputBytes,
        cause:
          typeof (failure.cause as { code?: unknown })?.code === "string"
            ? (failure.cause as { code: string }).code
            : "unknown",
      },
    );
  }
  return new JweSecurityError(
    "JWE_DECOMPRESSION_FAILED",
    "compressed payload could not be decompressed",
  );
}

/**
 * Streams DEFLATE output while enforcing two independent caps. The output is
 * never fully materialized before the checks: the zlib stream is destroyed on
 * the first chunk that exceeds either cap.
 */
export async function boundedInflate(
  compressed: Buffer | Uint8Array,
  options: BoundedInflateOptions,
): Promise<Buffer> {
  if (!Buffer.isBuffer(compressed)) {
    compressed = Buffer.from(compressed);
  }
  const input = compressed as Buffer;

  const maxPlaintextBytes = assertLimit(
    options.maxPlaintextBytes,
    "maxPlaintextBytes",
    true,
  );
  const maxRatio = assertLimit(options.maxRatio, "maxRatio", false);
  const format = options.format ?? "auto";

  if (format !== "auto" && format !== "raw" && format !== "zlib") {
    throw new JweSecurityError(
      "JWE_POLICY_INVALID",
      'format must be "auto", "raw", or "zlib"',
    );
  }

  const zlibOptions = {
    chunkSize: 1024,
    finishFlush: zlibConstants.Z_FINISH,
  };
  const useZlibWrapper =
    format === "zlib" ||
    (format === "auto" && looksLikeZlibWrapper(input));
  const makeInflater = useZlibWrapper
    ? () => createInflate(zlibOptions)
    : () => createInflateRaw(zlibOptions);

  try {
    return await inflateAttempt(
      makeInflater,
      input,
      maxPlaintextBytes,
      maxRatio,
    );
  } catch (error) {
    if (format !== "auto" || !(error instanceof AttemptFailure)) {
      throw wrapFailure(error);
    }
    if (!error.dataError || error.outputBytes !== 0) {
      throw wrapFailure(error);
    }

    const alternateFactory = useZlibWrapper
      ? () => createInflateRaw(zlibOptions)
      : () => createInflate(zlibOptions);
    try {
      return await inflateAttempt(
        alternateFactory,
        input,
        maxPlaintextBytes,
        maxRatio,
      );
    } catch (alternateError) {
      throw wrapFailure(alternateError);
    }
  }
}

export { boundedInflate as inflateWithLimits };
