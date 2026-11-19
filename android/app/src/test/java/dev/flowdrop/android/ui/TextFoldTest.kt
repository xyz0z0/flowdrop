package dev.flowdrop.android.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TextFoldTest {

    @Test
    fun `empty text needs no toggle`() {
        assertFalse(needsTextExpand(""))
    }

    @Test
    fun `whitespace only text needs no toggle`() {
        assertFalse(needsTextExpand("   "))
        assertFalse(needsTextExpand("\n\n"))
        assertFalse(needsTextExpand(" \r\n\t "))
    }

    @Test
    fun `short single line needs no toggle`() {
        assertFalse(needsTextExpand("你好"))
        assertFalse(needsTextExpand("hello world"))
        // Exactly one estimated line.
        assertFalse(needsTextExpand("a".repeat(18)))
    }

    @Test
    fun `three short lines need no toggle`() {
        assertFalse(needsTextExpand("第一行\n第二行\n第三行"))
    }

    @Test
    fun `exactly six estimated lines need no toggle`() {
        // Six lines of 18 chars each => 6 estimated lines, not more than the budget.
        assertFalse(needsTextExpand((1..6).joinToString("\n") { "x".repeat(18) }))
        // Six short lines still count as six.
        assertFalse(needsTextExpand((1..6).joinToString("\n") { "x" }))
    }

    @Test
    fun `seven short lines need a toggle`() {
        assertTrue(needsTextExpand((1..7).joinToString("\n") { "x" }))
    }

    @Test
    fun `single line over 280 chars needs a toggle`() {
        // Both the character threshold and the line estimate agree here.
        assertTrue(needsTextExpand("a".repeat(281)))
        // 108 chars wraps to 6 estimated lines of 18 chars: still below the budget.
        assertFalse(needsTextExpand("a".repeat(108)))
    }

    @Test
    fun `crlf and mixed newlines are normalized`() {
        // 7 CRLF-separated short lines: normalized to 7 lines => toggle.
        assertTrue(needsTextExpand((1..7).joinToString("\r\n") { "x" }))
        // 6 CR-only separated short lines stay under the budget.
        assertFalse(needsTextExpand((1..6).joinToString("\r") { "x" }))
        // Mixed endings behave the same as "\n".
        assertTrue(needsTextExpand("x\r\nx\rx\nx\nx\nx\nx"))
    }

    @Test
    fun `very long single line needs a toggle`() {
        assertTrue(needsTextExpand("长".repeat(1000)))
    }

    @Test
    fun `long text with surrounding whitespace needs a toggle`() {
        assertTrue(needsTextExpand("\n\n  " + "长".repeat(300) + "  \n\n"))
    }
}
