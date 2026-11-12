import type { FormEntry, PreparedRequest, SubmitPlan } from "./types.ts";
import { encodeFormComponent, withQuery } from "./url.ts";

const CRLF = "\r\n";

function urlencodedBody(entries: readonly FormEntry[]): string {
  return entries
    .filter((entry) => entry.name !== "")
    .map((entry) => {
      const value = typeof entry.value === "string" ? entry.value : "";
      return `${encodeFormComponent(entry.name)}=${encodeFormComponent(value)}`;
    })
    .join("&");
}

function textPlainBody(entries: readonly FormEntry[]): string {
  return entries
    .filter((entry) => entry.name !== "")
    .map((entry) => {
      const value = typeof entry.value === "string" ? entry.value : "";
      return `${entry.name}=${value}`;
    })
    .join(CRLF);
}

function makeBoundary(): string {
  const random = crypto.getRandomValues(new Uint8Array(16));
  const hex = Array.from(random, (byte) => byte.toString(16).padStart(2, "0")).join("");
  return `----formplan${hex}`;
}

async function readBlob(blob: Blob): Promise<Uint8Array> {
  const buffer = await blob.arrayBuffer();
  return new Uint8Array(buffer);
}

function encodeText(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

function pushBytes(chunks: Uint8Array[], bytes: Uint8Array): void {
  chunks.push(bytes);
}

async function multipartBody(
  entries: readonly FormEntry[],
  boundary: string,
): Promise<Uint8Array> {
  const chunks: Uint8Array[] = [];
  for (const entry of entries) {
    if (entry.name === "") {
      continue;
    }
    pushBytes(chunks, encodeText(`--${boundary}${CRLF}`));
    if (typeof entry.value === "string") {
      pushBytes(
        chunks,
        encodeText(
          `content-disposition: form-data; name="${escapeHeader(entry.name)}"${CRLF}${CRLF}`,
        ),
      );
      pushBytes(chunks, encodeText(entry.value));
      pushBytes(chunks, encodeText(CRLF));
    } else {
      const filename = entry.filename ?? entry.value.name ?? "blob";
      const contentType = entry.value.type || "application/octet-stream";
      pushBytes(
        chunks,
        encodeText(
          `content-disposition: form-data; name="${escapeHeader(entry.name)}"; ` +
            `filename="${escapeHeader(filename)}"${CRLF}content-type: ${contentType}${CRLF}${CRLF}`,
        ),
      );
      pushBytes(chunks, await readBlob(entry.value));
      pushBytes(chunks, encodeText(CRLF));
    }
  }
  pushBytes(chunks, encodeText(`--${boundary}--${CRLF}`));
  return concatBytes(chunks);
}

function escapeHeader(value: string): string {
  return value.replace(/["\\\r\n]/g, (ch) => `\\${ch}`);
}

function concatBytes(chunks: readonly Uint8Array[]): Uint8Array {
  const total = chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
  const output = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return output;
}

/**
 * Build the dispatch request from an approved plan. The target URL always
 * comes from the frozen plan, never from the live document.
 */
export async function prepareRequest(plan: SubmitPlan): Promise<PreparedRequest> {
  const target = new URL(plan.target.absoluteUrl);

  if (plan.target.method === "GET") {
    const query = urlencodedBody(plan.entries);
    const url = withQuery(target, query).href;
    return { url, method: "GET", headers: {}, body: null };
  }

  switch (plan.target.enctype) {
    case "multipart/form-data": {
      const boundary = makeBoundary();
      const body = await multipartBody(plan.entries, boundary);
      return {
        url: target.href,
        method: "POST",
        headers: {
          "content-type": `multipart/form-data; boundary=${boundary}`,
        },
        body,
      };
    }
    case "text/plain": {
      return {
        url: target.href,
        method: "POST",
        headers: { "content-type": "text/plain;charset=UTF-8" },
        body: textPlainBody(plan.entries),
      };
    }
    case "application/x-www-form-urlencoded":
    default: {
      return {
        url: target.href,
        method: "POST",
        headers: {
          "content-type": "application/x-www-form-urlencoded;charset=UTF-8",
        },
        body: urlencodedBody(plan.entries),
      };
    }
  }
}
