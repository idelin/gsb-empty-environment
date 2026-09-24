import { createHash } from "node:crypto";
import { utf16Length } from "./utf16.ts";

/**
 * Content digest of a base text. Binds both the UTF-16 length and the exact
 * UTF-8 byte content, so a same-length-but-different base can never validate.
 */
export function digestText(text: string): string {
  const hash = createHash("sha256");
  hash.update(`utf16:${utf16Length(text)}:`, "utf8");
  hash.update(text, "utf8");
  return hash.digest("hex");
}
