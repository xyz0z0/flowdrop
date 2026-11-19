package dev.flowdrop.android.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class TextLinksTest {

    /** Rebuilds the text around the found links, exactly like the card renders it. */
    private fun rebuild(text: String, links: List<HttpLink>): String = buildString {
        var cursor = 0
        for (link in links) {
            append(text, cursor, link.start)
            append(text, link.start, link.end)
            cursor = link.end
        }
        append(text, cursor, text.length)
    }

    /** The text between the links (before, between, after). */
    private fun gapTexts(text: String, links: List<HttpLink>): List<String> {
        val gaps = mutableListOf<String>()
        var cursor = 0
        for (link in links) {
            gaps += text.substring(cursor, link.start)
            cursor = link.end
        }
        gaps += text.substring(cursor)
        return gaps
    }

    private fun assertLinks(text: String, expected: List<String>): List<HttpLink> {
        val links = findHttpLinks(text)
        assertEquals(expected, links.map { it.url })
        for (link in links) {
            assertEquals(link.url, text.substring(link.start, link.end))
            assertTrue(link.start < link.end)
        }
        // The slices cover the original text in order: nothing is rewritten,
        // dropped or duplicated by finding links.
        assertEquals(text, rebuild(text, links))
        return links
    }

    @Test
    fun `two goofish urls keep their full query and offsets`() {
        val first =
            "https://www.goofish.com/item?spm=a21ybx.personal.feeds.1.37447cdeNE1KeA&id=1099181190030&categoryId=50025387"
        val second =
            "https://www.goofish.com/item?spm=a21ybx.personal.feeds.2.37447cdeNE1KeA&id=1101369016573&categoryId=50025387"
        val text = "银驰 GTX1660 v 友 820r\n$first\n\n威刚 DDR4 2666 内存条 8g*3 v 友 680r\n$second"

        val links = assertLinks(text, listOf(first, second))

        assertEquals(2, links.size)
        assertEquals(text.indexOf(first), links[0].start)
        assertEquals(text.indexOf(first) + first.length, links[0].end)
        assertEquals(text.indexOf(second), links[1].start)
        assertEquals(text.indexOf(second) + second.length, links[1].end)
        // Blank line and surrounding Chinese text are untouched.
        assertEquals(
            listOf(
                "银驰 GTX1660 v 友 820r\n",
                "\n\n威刚 DDR4 2666 内存条 8g*3 v 友 680r\n",
                "",
            ),
            gapTexts(text, links),
        )
        // Two separate, non-overlapping ranges.
        assertTrue(links[0].end <= links[1].start)
    }

    @Test
    fun `crlf text keeps its line endings around the links`() {
        val text = "标题\r\n\r\nhttps://a.example/x?id=1&\r\nhttps://b.example/y?v=2"
        val links = assertLinks(text, listOf("https://a.example/x?id=1&", "https://b.example/y?v=2"))
        assertEquals(listOf("标题\r\n\r\n", "\r\n", ""), gapTexts(text, links))
    }

    @Test
    fun `query fragment and percent escapes stay inside the url`() {
        val text = "见 https://example.com/a/b?q=1&r=2%20x#frag ，再看 https://e.example/z"
        assertLinks(
            text,
            listOf("https://example.com/a/b?q=1&r=2%20x#frag", "https://e.example/z"),
        )
    }

    @Test
    fun `links are split on the same line and across lines`() {
        assertLinks(
            "https://a.example/1中文 https://b.example/2\nhttps://c.example/3",
            listOf("https://a.example/1中文", "https://b.example/2", "https://c.example/3"),
        )
        assertLinks("看：https://a.example/x。", listOf("https://a.example/x"))
        assertLinks(
            "https://a.example/x，https://b.example/y",
            listOf("https://a.example/x", "https://b.example/y"),
        )
    }

    @Test
    fun `cjk and ascii ending punctuation is not part of the link`() {
        assertLinks("https://a.example/end。后文", listOf("https://a.example/end"))
        assertLinks("https://a.example/end，", listOf("https://a.example/end"))
        assertLinks("https://a.example/end；：！？、…", listOf("https://a.example/end"))
        assertLinks("https://a.example/end.", listOf("https://a.example/end"))
        assertLinks("https://a.example/end,;:!?", listOf("https://a.example/end"))
        assertLinks("https://a.example/end\"", listOf("https://a.example/end"))
        assertLinks("https://a.example/end'", listOf("https://a.example/end"))
    }

    @Test
    fun `wrapping brackets are dropped but balanced url brackets are kept`() {
        assertLinks("（https://a.example/x）", listOf("https://a.example/x"))
        assertLinks("（https://a.example/x)) tail）", listOf("https://a.example/x"))
        assertLinks("【https://a.example/x】", listOf("https://a.example/x"))
        assertLinks("「https://a.example/x」", listOf("https://a.example/x"))
        // Balanced ASCII brackets may be part of the path/query.
        assertLinks("https://example.com/a_(b)?q=1&r=2", listOf("https://example.com/a_(b)?q=1&r=2"))
        assertLinks("（https://example.com/a_(b)）", listOf("https://example.com/a_(b)"))
    }

    @Test
    fun `ascii parentheses and brackets wrapping a url are matched`() {
        // LINK-02F F1: `(` and `[` are no longer URL characters, so a wrapped or
        // pasted-markdown link matches and the wrapper stays plain text.
        assertLinks("(https://a.example/x)", listOf("https://a.example/x"))
        assertLinks("[https://a.example/x]", listOf("https://a.example/x"))
        assertLinks("[标题](https://a.example/x)", listOf("https://a.example/x"))
        assertLinks("看(https://a.example/x)", listOf("https://a.example/x"))
        assertLinks("((https://a.example/x))", listOf("https://a.example/x"))
        assertLinks("见 [https://a.example/x] 和 (https://b.example/y)", listOf("https://a.example/x", "https://b.example/y"))
        // The outer wrapper is dropped while brackets inside the URL stay.
        assertLinks("(https://example.com/a_(b))", listOf("https://example.com/a_(b)"))
        assertLinks("[https://example.com/a_(b)]", listOf("https://example.com/a_(b)"))
        // A wrapping bracket right after an ordinary character also matches.
        assertLinks("看(https://a.example/x)，后文", listOf("https://a.example/x"))
    }

    @Test
    fun `unicode whitespace and bom terminate the url`() {
        // LINK-02F F2: Java `\s` is ASCII-only; Unicode spaces must end the body
        // exactly like they do on the web client.
        assertLinks("https://a.example/x\u00a0下一段", listOf("https://a.example/x"))
        assertLinks(
            "https://a.example/x\u00a0https://b.example/y",
            listOf("https://a.example/x", "https://b.example/y"),
        )
        assertLinks("https://a.example/x\u2009y", listOf("https://a.example/x"))
        assertLinks("https://a.example/x\ufeffy", listOf("https://a.example/x"))
        assertLinks("https://a.example/x\u200ay", listOf("https://a.example/x"))
        assertLinks("https://a.example/x\u2028y", listOf("https://a.example/x"))
        assertLinks("https://a.example/x\u2029y", listOf("https://a.example/x"))
        assertLinks("https://a.example/x\u3000y", listOf("https://a.example/x"))
        // The gaps keep the original separators, so nothing is rewritten.
        val text = "https://a.example/x\u00a0https://b.example/y"
        assertEquals(listOf("", "\u00a0", ""), gapTexts(text, findHttpLinks(text)))
    }

    @Test
    fun `underscore before the scheme is not a link`() {
        // LINK-02F F3: `_` is a URL character on both ends now.
        assertLinks("_https://a.example/x", emptyList())
        assertLinks("a_b https://yes.example/b", listOf("https://yes.example/b"))
    }

    @Test
    fun `only explicit http and https are links`() {
        assertLinks("ftp://a.example/x", emptyList())
        assertLinks("nothttp://a.example/x", emptyList())
        assertLinks("xhttps://a.example/x", emptyList())
        assertLinks("www.goofish.com/item?id=1", emptyList())
        assertLinks("mailto:a@b.example", emptyList())
        assertLinks("纯文本，没有任何网址。", emptyList())
        // A real URL preceded by a non-URL character still matches.
        assertLinks("见https://yes.example/b", listOf("https://yes.example/b"))
        assertLinks("a=https://yes.example/b", listOf("https://yes.example/b"))
        assertLinks("：https://yes.example/b", listOf("https://yes.example/b"))
    }

    @Test
    fun `scheme without body is not a link`() {
        assertLinks("https://", emptyList())
        assertLinks("http:// ", emptyList())
        assertLinks("https://。", emptyList())
    }

    @Test
    fun `plain text and blank lines produce no links`() {
        assertLinks("", emptyList())
        assertLinks("\n\n", emptyList())
        assertLinks("纯文本，没有任何网址。\n\n第二段", emptyList())
        assertEquals(emptyList<HttpLink>(), findHttpLinks("   \r\n\t "))
    }
}
