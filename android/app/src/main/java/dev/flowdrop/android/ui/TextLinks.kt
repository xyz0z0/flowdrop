package dev.flowdrop.android.ui

/**
 * One http/https URL found inside a plain-text body.
 *
 * [start] is inclusive and [end] is exclusive, both as indices into the
 * original text, so `text.substring(start, end) == url` always holds.
 */
data class HttpLink(val start: Int, val end: Int, val url: String)

// A link body stops at whitespace, at quotes/brackets used to wrap a pasted
// URL, and at CJK sentence punctuation / full-width brackets / ideographic
// space. Java's `\s` is ASCII-only, so the Unicode space separators and the BOM
// are added explicitly to match the web client. Everything else (including
// `&`, `%`, `#`, `?` and ASCII brackets) stays inside the URL.
private const val URL_STOP =
    "\\s\\p{Z}\\ufeff<>\"'`，。；：！？、…（）【】「」『』《》〈〉〔〕｛｝［］\u3000"

// A link may only start where no URL character precedes it, so "xhttps://…" and
// "_https://…" stay plain text while "见https://…", "=https://…" and a wrapping
// "(" / "[" still match; the closing side is only used for the tail trim.
private const val URL_CHAR = "A-Za-z0-9._\\-~:/?#\\]@!\\$&)*+,;"

private val HTTP_LINK = Regex("(?<![" + URL_CHAR + "])https?://[^" + URL_STOP + "]+")

// ASCII sentence punctuation that ends the sentence rather than the URL.
// Quotes and CJK punctuation never reach here: they already stop the body.
private const val TRAILING_PUNCTUATION = ".,;:!?"

// ASCII brackets may belong to the URL ("a_(b)?q=1") when balanced; a surplus
// closing bracket is the wrapper's and is dropped.
private val BRACKET_PAIRS = mapOf(')' to '(', ']' to '[', '}' to '{')

private const val HTTP_SCHEME = "http://"
private const val HTTPS_SCHEME = "https://"

/**
 * Finds every http/https URL in [text], in order.
 *
 * Links are detected in place and the text is never rewritten, so anything
 * outside the returned ranges — blank lines, CRLF, CJK punctuation — is
 * untouched and concatenating the slices back always reproduces [text]. Only
 * explicit http/https URLs are recognised: bare domains, `ftp://`, `mailto:`
 * and other schemes stay plain text.
 */
fun findHttpLinks(text: String): List<HttpLink> {
    if (text.isEmpty()) return emptyList()
    val links = mutableListOf<HttpLink>()
    for (match in HTTP_LINK.findAll(text)) {
        val url = trimLinkTail(match.value)
        val schemeLength =
            if (url.startsWith(HTTPS_SCHEME)) HTTPS_SCHEME.length else HTTP_SCHEME.length
        // "https://" on its own has nothing to open.
        if (url.length <= schemeLength) continue
        links += HttpLink(match.range.first, match.range.first + url.length, url)
    }
    return links
}

/** Drops trailing sentence punctuation and unbalanced closing brackets. */
private fun trimLinkTail(raw: String): String {
    var end = raw.length
    while (end > 0) {
        val last = raw[end - 1]
        if (last in TRAILING_PUNCTUATION) {
            end--
            continue
        }
        val opener = BRACKET_PAIRS[last]
        if (opener != null) {
            val kept = raw.substring(0, end)
            if (kept.count { it == last } > kept.count { it == opener }) {
                end--
                continue
            }
        }
        break
    }
    return raw.substring(0, end)
}
