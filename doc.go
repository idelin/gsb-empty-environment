// Package qstr embeds arbitrary Unicode text into custom double-quoted
// strings and decodes them back.
//
// Wire format: a record is a double-quoted string. Inside the quotes the
// backslash, the double quote and control characters (U+0000-U+001F and
// U+007F) must be escaped; every other character appears as itself in
// UTF-8. Only three escape forms exist:
//
//	\\        backslash
//	\"        double quote
//	\uXXXX    any code point, hex may be upper or lower case
//
// A high surrogate escape (\uD800-\uDBFF) is only legal when immediately
// followed by a low surrogate escape (\uDC00-\uDFFF); the pair decodes to
// the combined code point. Lone surrogates, unknown escapes, invalid hex
// digits, raw control characters and invalid UTF-8 are rejected. All
// error offsets are byte offsets into the original input.
//
// Decoding is single-pass: escape sequences are resolved exactly once, so
// the decoded output is never re-interpreted and cannot gain new escapes.
//
// For streaming use see StreamEncoder (bounded output buffer with
// backpressure and context cancellation) and StreamDecoder (checkpointed
// input consumption, escapes and surrogate pairs may span chunks, and
// error recovery resynchronizes on a caller-specified record separator;
// a separator occurring inside a legal escape sequence is not a boundary).
package qstr
