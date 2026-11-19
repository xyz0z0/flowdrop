package dev.flowdrop.android.ui

// Rough number of characters that fit on one line of a narrow phone screen
// (CJK text). Used only to estimate a text height, so it is kept generous.
private const val LINE_CHARS = 18
// Lines shown before a text entry is collapsed.
private const val COLLAPSED_LINES = 6
// Stable character threshold: above this a text entry always gets the toggle.
private const val LONG_TEXT_CHARS = 280

/**
 * Whether a text entry needs an expand/collapse toggle.
 *
 * This is an estimate threshold only: [LINE_CHARS] approximates how many
 * Chinese characters fit on one line of a narrow phone screen. It decides
 * whether to render the toggle button; it does not take part in real layout.
 *
 * The decision is deliberately conservative: only text that is long by
 * character count, or that clearly wraps past the collapsed line budget, is
 * folded. Surrounding whitespace is ignored so short multi-line messages stay
 * short and no text is ever clipped without a way to reveal it.
 */
fun needsTextExpand(raw: String): Boolean {
    val text = raw.replace("\r\n", "\n").replace("\r", "\n").trim()
    if (text.isEmpty()) return false
    if (text.length > LONG_TEXT_CHARS) return true
    var lines = 0
    for (line in text.split('\n')) {
        lines += maxOf(1, (line.length + LINE_CHARS - 1) / LINE_CHARS)
    }
    return lines > COLLAPSED_LINES
}
