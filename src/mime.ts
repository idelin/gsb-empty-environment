/**
 * MIME classification, extension mapping and content sniffing used by the
 * download type guard. All tables are explicit and fail closed: a MIME type
 * without a known extension mapping cannot pass the extension check.
 */

const MIME_ALIASES: Record<string, string> = {
  "application/x-pdf": "application/pdf",
  "application/x-javascript": "text/javascript",
  "application/x-ecmascript": "text/javascript",
  "text/x-javascript": "text/javascript",
  "text/ecmascript": "text/javascript",
  "application/x-sh": "application/x-sh",
};

/** Strip parameters (e.g. `; charset=...`) and normalize aliases/case. */
export function canonicalMime(contentType: string): string {
  const base = contentType.split(";", 1)[0]!.trim().toLowerCase();
  return MIME_ALIASES[base] ?? base;
}

/** Allowed file extensions (without leading dot) for each declared MIME type. */
export const EXTENSIONS_BY_MIME: Readonly<Record<string, readonly string[]>> = {
  "application/pdf": ["pdf"],
  "text/html": ["html", "htm"],
  "application/xhtml+xml": ["xhtml", "xht"],
  "image/svg+xml": ["svg"],
  "application/xml": ["xml"],
  "text/plain": ["txt", "text", "log", "csv"],
  "text/css": ["css"],
  "text/javascript": ["js", "mjs"],
  "application/javascript": ["js", "mjs"],
  "application/ecmascript": ["js", "mjs"],
  "application/json": ["json"],
  "image/png": ["png"],
  "image/jpeg": ["jpg", "jpeg"],
  "image/gif": ["gif"],
  "image/webp": ["webp"],
  "image/bmp": ["bmp"],
  "image/x-icon": ["ico"],
  "audio/mpeg": ["mp3"],
  "audio/wav": ["wav"],
  "video/mp4": ["mp4"],
  "application/zip": ["zip"],
  "application/java-archive": ["jar"],
  "application/vnd.oasis.opendocument.text": ["odt"],
  "application/vnd.oasis.opendocument.spreadsheet": ["ods"],
  "application/vnd.oasis.opendocument.presentation": ["odp"],
  "application/x-7z-compressed": ["7z"],
  "application/x-rar-compressed": ["rar"],
  "application/vnd.rar": ["rar"],
  "application/x-tar": ["tar"],
  "application/gzip": ["gz"],
  "application/x-msdownload": ["exe", "dll", "sys", "msi"],
  "application/x-dosexec": ["exe", "dll", "sys", "msi"],
  "application/x-msi": ["msi"],
  "application/x-elf": ["elf"],
  "application/x-sharedlib": ["so"],
  "application/x-mach-binary": ["dylib"],
  "application/x-sh": ["sh"],
  "application/x-csh": ["csh"],
  "application/x-python-code": ["pyc", "pyo"],
  "text/x-python": ["py"],
  "text/x-perl": ["pl"],
  "text/x-ruby": ["rb"],
  "text/x-shellscript": ["sh"],
  "application/java-vm": ["class"],
  "application/octet-stream": ["bin", "dat", "blob"],
};

export function extensionsFor(mime: string): readonly string[] | undefined {
  return EXTENSIONS_BY_MIME[mime];
}

/** Reverse lookup: MIME type implied by a file extension. */
export function mimeForExtension(ext: string): string | undefined {
  const needle = ext.toLowerCase();
  for (const [mime, exts] of Object.entries(EXTENSIONS_BY_MIME)) {
    if (exts.includes(needle)) return mime;
  }
  return undefined;
}

/**
 * Types that must always be served with `Content-Disposition: attachment`
 * because browsers may execute or interpret them (active content / scripts),
 * or because they are native executable binaries.
 */
const INTERPRETABLE_TYPES = new Set<string>([
  "text/html",
  "application/xhtml+xml",
  "image/svg+xml",
  "text/xml",
  "application/xml",
  "text/css",
  "text/javascript",
  "application/javascript",
  "application/ecmascript",
  "application/pdf",
]);

const EXECUTABLE_TYPES = new Set<string>([
  "application/x-msdownload",
  "application/x-dosexec",
  "application/x-msi",
  "application/x-elf",
  "application/x-sharedlib",
  "application/x-mach-binary",
  "application/x-sh",
  "application/x-csh",
  "application/x-python-code",
  "text/x-python",
  "text/x-perl",
  "text/x-ruby",
  "text/x-shellscript",
  "application/java-vm",
]);

export type DispositionReason =
  | "interpretable"
  | "executable"
  | "unknown-binary"
  | "safe";

export interface DispositionDecision {
  disposition: "attachment" | "inline";
  reason: DispositionReason;
}

/**
 * Disposition is derived solely from the declared MIME type. Request headers
 * and query parameters never influence this decision.
 */
export function decideDisposition(declaredMime: string): DispositionDecision {
  if (INTERPRETABLE_TYPES.has(declaredMime)) {
    return { disposition: "attachment", reason: "interpretable" };
  }
  if (EXECUTABLE_TYPES.has(declaredMime)) {
    return { disposition: "attachment", reason: "executable" };
  }
  if (declaredMime === "application/octet-stream") {
    return { disposition: "attachment", reason: "unknown-binary" };
  }
  return { disposition: "inline", reason: "safe" };
}

/** MIME types whose on-disk formats share the same container signature. */
const ZIP_FAMILY = new Set([
  "application/zip",
  "application/java-archive",
  "application/vnd.oasis.opendocument.text",
  "application/vnd.oasis.opendocument.spreadsheet",
  "application/vnd.oasis.opendocument.presentation",
]);

const TEXTISH_TYPES = new Set([
  "text/html",
  "application/xhtml+xml",
  "image/svg+xml",
  "text/xml",
  "application/xml",
  "text/plain",
  "text/css",
  "text/javascript",
  "application/javascript",
  "application/ecmascript",
  "application/json",
  "text/x-python",
  "text/x-perl",
  "text/x-ruby",
  "text/x-shellscript",
  "application/x-sh",
  "application/x-csh",
]);

function startsWith(data: Uint8Array, signature: number[]): boolean {
  if (data.length < signature.length) return false;
  return signature.every((byte, index) => data[index] === byte);
}

function decodeAsciiPrefix(data: Uint8Array, maxBytes = 512): string {
  const end = Math.min(data.length, maxBytes);
  let out = "";
  for (let i = 0; i < end; i += 1) {
    const byte = data[i]!;
    if (byte === 0) break;
    out += byte >= 32 && byte < 127 ? String.fromCharCode(byte) : " ";
  }
  return out;
}

/**
 * Sniff the actual type from magic bytes. Returns a canonical MIME type,
 * `"text"` for leading-text content, or `undefined` when unrecognized.
 */
export function sniffType(data: Uint8Array): string | "text" | undefined {
  if (data.length === 0) return undefined;

  if (startsWith(data, [0x25, 0x50, 0x44, 0x46])) return "application/pdf";
  if (startsWith(data, [0x89, 0x50, 0x4e, 0x47])) return "image/png";
  if (startsWith(data, [0xff, 0xd8, 0xff])) return "image/jpeg";
  if (startsWith(data, [0x47, 0x49, 0x46, 0x38])) return "image/gif";
  if (
    data.length >= 12 &&
    startsWith(data, [0x52, 0x49, 0x46, 0x46]) &&
    data[8] === 0x57 &&
    data[9] === 0x45 &&
    data[10] === 0x42 &&
    data[11] === 0x50
  ) {
    return "image/webp";
  }
  if (startsWith(data, [0x42, 0x4d])) return "image/bmp";
  if (startsWith(data, [0x00, 0x00, 0x01, 0x00])) return "image/x-icon";
  if (startsWith(data, [0x49, 0x44, 0x33]) || startsWith(data, [0xff, 0xfb])) {
    return "audio/mpeg";
  }
  if (startsWith(data, [0x52, 0x49, 0x46, 0x46]) && data.length >= 12) {
    return "audio/wav";
  }
  if (
    data.length >= 12 &&
    data[4] === 0x66 &&
    data[5] === 0x74 &&
    data[6] === 0x79 &&
    data[7] === 0x70
  ) {
    return "video/mp4";
  }
  if (startsWith(data, [0x50, 0x4b, 0x03, 0x04]) || startsWith(data, [0x50, 0x4b, 0x05, 0x06])) {
    return "application/zip";
  }
  if (startsWith(data, [0x37, 0x7a, 0xbc, 0xaf, 0x27, 0x1c])) {
    return "application/x-7z-compressed";
  }
  if (startsWith(data, [0x52, 0x61, 0x72, 0x21])) return "application/vnd.rar";
  if (startsWith(data, [0x75, 0x73, 0x74, 0x61, 0x72])) return "application/x-tar";
  if (startsWith(data, [0x1f, 0x8b])) return "application/gzip";
  if (startsWith(data, [0x4d, 0x5a])) return "application/x-dosexec";
  if (startsWith(data, [0x7f, 0x45, 0x4c, 0x46])) return "application/x-elf";
  if (
    startsWith(data, [0xcf, 0xfa, 0xed, 0xfe]) ||
    startsWith(data, [0xce, 0xfa, 0xed, 0xfe]) ||
    startsWith(data, [0xfe, 0xed, 0xfa, 0xce])
  ) {
    return "application/java-vm";
  }
  if (startsWith(data, [0xca, 0xfe, 0xba, 0xbe])) return "application/java-vm";

  const prefix = decodeAsciiPrefix(data);
  if (prefix.includes("<?xml")) {
    if (prefix.includes("<svg")) return "image/svg+xml";
    return "application/xml";
  }
  if (/^\s*<!(doctype|html)\b/i.test(prefix) || /<html[\s>]/i.test(prefix)) {
    return "text/html";
  }

  if (
    startsWith(data, [0xef, 0xbb, 0xbf]) ||
    startsWith(data, [0xff, 0xfe]) ||
    startsWith(data, [0xfe, 0xff])
  ) {
    return "text";
  }
  if (data[0]! >= 0x09 && data[0]! <= 0x7e) return "text";

  return undefined;
}

/** Whether a sniffed type is compatible with the declared type. */
export function sniffMatches(
  declaredMime: string,
  sniffed: string | "text" | undefined,
): boolean {
  if (sniffed === undefined) return true;
  if (sniffed === "text") return TEXTISH_TYPES.has(declaredMime);
  if (sniffed === declaredMime) return true;
  if (sniffed === "application/zip" && ZIP_FAMILY.has(declaredMime)) return true;
  if (declaredMime === "application/octet-stream") return sniffed !== "text";
  return false;
}
