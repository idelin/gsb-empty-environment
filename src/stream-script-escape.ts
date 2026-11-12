const DANGEROUS_PATTERNS: readonly string[] = ["</script", "<script", "<!--"];

const LINE_SEPARATOR = String.fromCharCode(0x2028);
const PARAGRAPH_SEPARATOR = String.fromCharCode(0x2029);

function matchesAnyPatternPrefix(candidate: string): boolean {
  return DANGEROUS_PATTERNS.some((pattern) => pattern.startsWith(candidate));
}

function isFullPattern(candidate: string): boolean {
  return DANGEROUS_PATTERNS.includes(candidate);
}

/**
 * Streaming escaper for JSON text embedded in an inline script element.
 *
 * Accepts arbitrarily split byte chunks (including splits in the middle of a
 * UTF-8 multi-byte sequence) and emits escaped text. The following are
 * escaped with a cross-chunk state machine:
 *   - "</script", "<script", "<!--" (case-insensitive): "<" -> "\u003C"
 *   - U+2028 / U+2029: -> "\u2028" / "\u2029"
 *
 * The escaped output still parses (via JSON.parse) to the original value.
 * Bytes that may still turn into a dangerous sequence are held back and
 * never released before they are disambiguated.
 */
export class StreamingScriptJsonEscaper {
  #decoder = new TextDecoder("utf-8");
  #pending = "";
  #ended = false;

  push(chunk: Uint8Array | string): string {
    if (this.#ended) {
      throw new Error("cannot push after end()");
    }
    const text =
      typeof chunk === "string"
        ? chunk
        : this.#decoder.decode(chunk, { stream: true });
    return this.#consume(text);
  }

  end(): string {
    if (this.#ended) {
      throw new Error("end() called twice");
    }
    this.#ended = true;
    const out = this.#consume(this.#decoder.decode()) + this.#pending;
    this.#pending = "";
    return out;
  }

  #consume(text: string): string {
    let out = "";
    let index = 0;
    while (index < text.length) {
      if (this.#pending.length === 0) {
        const ch = text[index];
        if (ch === "<") {
          this.#pending = "<";
          index += 1;
        } else if (ch === LINE_SEPARATOR) {
          out += "\\u2028";
          index += 1;
        } else if (ch === PARAGRAPH_SEPARATOR) {
          out += "\\u2029";
          index += 1;
        } else {
          let next = index + 1;
          while (
            next < text.length &&
            text[next] !== "<" &&
            text[next] !== LINE_SEPARATOR &&
            text[next] !== PARAGRAPH_SEPARATOR
          ) {
            next += 1;
          }
          out += text.slice(index, next);
          index = next;
        }
      } else {
        const candidate = (this.#pending + text[index]).toLowerCase();
        if (matchesAnyPatternPrefix(candidate)) {
          this.#pending += text[index];
          index += 1;
          if (isFullPattern(this.#pending.toLowerCase())) {
            out += "\\u003C" + this.#pending.slice(1);
            this.#pending = "";
          }
        } else {
          out += this.#pending;
          this.#pending = "";
        }
      }
    }
    return out;
  }
}

export function escapeInlineScriptJson(text: string): string {
  const escaper = new StreamingScriptJsonEscaper();
  return escaper.push(text) + escaper.end();
}
