/**
 * Splits a text message into plain-text and clickable http/https URL pieces.
 *
 * Only explicit `http://` / `https://` URLs are recognised: bare domains,
 * `ftp://` and `mailto:` stay plain text. Concatenating the pieces in order
 * always yields the original string, so copy and expand/collapse are unaffected.
 */

/** A run of plain text, or one clickable http/https URL. */
export type TextLinkPiece =
  | { kind: 'text'; value: string }
  | { kind: 'url'; value: string };

// Trailing characters that never belong to the link: CJK/full-width sentence
// punctuation, full-width brackets and the ideographic space.
const TAIL_STOP = new Set([...'，。；：！？、…', ...'）】」』》〉〕｝］', '\u3000']);
// Trailing ASCII sentence punctuation is trimmed one character at a time.
const TAIL_ASCII = new Set([...'.,;:!?\'"']);
// Closing bracket -> opener; trimmed only while the closing side is unbalanced.
const CLOSERS: Record<string, string> = { ')': '(', ']': '[', '}': '{' };

// A URL must start the text or follow a non-URL character, so `xhttps://…` stays
// plain text while `见https://…` is a link. ASCII `(` and `[` are left out of
// that set so `(url)`, `[url]` and markdown `[标题](url)` match; the closing
// side stays in it, feeding the balanced-bracket trim below.
const URL_RE =
  /(^|[^A-Za-z0-9\-._~:/?#\]@!$&)*+,;])https?:\/\/[^\s<>"'`，。；：！？、…（）【】「」『』《》〈〉〔〕｛｝［］\u3000]+/gu;

function countOf(text: string, char: string): number {
  let count = 0;
  for (const c of text) if (c === char) count += 1;
  return count;
}

/** Drops trailing punctuation and unbalanced closing brackets from a match. */
function trimUrlTail(raw: string): string {
  let url = raw;
  for (;;) {
    const last = url.charAt(url.length - 1);
    if (!last) return url;
    if (TAIL_STOP.has(last) || TAIL_ASCII.has(last)) {
      url = url.slice(0, -1);
      continue;
    }
    const opener = CLOSERS[last];
    if (opener && countOf(url, last) > countOf(url, opener)) {
      url = url.slice(0, -1);
      continue;
    }
    return url;
  }
}

/** True when something follows the `//` scheme separator. */
function hasBody(url: string): boolean {
  return url.length > url.indexOf('//') + 2;
}

export function splitTextLinks(text: string): TextLinkPiece[] {
  const pieces: TextLinkPiece[] = [];
  let cursor = 0;
  for (const match of text.matchAll(URL_RE)) {
    const leading = match[1] ?? '';
    const start = (match.index ?? 0) + leading.length;
    const url = trimUrlTail(match[0].slice(leading.length));
    if (!hasBody(url)) continue;
    if (start > cursor) pieces.push({ kind: 'text', value: text.slice(cursor, start) });
    pieces.push({ kind: 'url', value: url });
    cursor = start + url.length;
  }
  if (cursor < text.length) pieces.push({ kind: 'text', value: text.slice(cursor) });
  return pieces;
}
