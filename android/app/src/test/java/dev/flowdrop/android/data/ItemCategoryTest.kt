package dev.flowdrop.android.data

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * Pins the client side of the file-category filter: the frozen chip order, the
 * "never classify on the client" rule ([ItemCategory.matches] only compares the
 * Hub-assigned value) and the required `category` field of an item.
 */
class ItemCategoryTest {

    private val json = Json { ignoreUnknownKeys = true }

    private fun item(category: String) = Item(
        id = "i-$category",
        type = "file",
        category = category,
        senderDevice = "PC",
        createdAt = 1,
    )

    @Test
    fun `chip order matches the frozen list`() {
        assertEquals(
            listOf("all", "text", "link", "image", "video", "audio", "document", "archive", "other"),
            ItemCategory.filters,
        )
    }

    @Test
    fun `all matches every item and a filter matches only its own category`() {
        val video = item(ItemCategory.VIDEO)
        assertTrue(ItemCategory.matches(video, ItemCategory.ALL))
        assertTrue(ItemCategory.matches(video, ItemCategory.VIDEO))
        assertFalse(ItemCategory.matches(video, ItemCategory.IMAGE))
        // An unknown/empty server value is never inferred, only hidden.
        assertFalse(ItemCategory.matches(item(""), ItemCategory.DOCUMENT))
        assertTrue(ItemCategory.matches(item(""), ItemCategory.ALL))
    }

    @Test
    fun `item parses the server category`() {
        val parsed = json.decodeFromString(
            Item.serializer(),
            """{"id":"a","type":"file","category":"archive","sender_device":"PC","created_at":1}""",
        )
        assertEquals("archive", parsed.category)
    }

    @Test
    fun `item without category is rejected instead of defaulting`() {
        try {
            json.decodeFromString(
                Item.serializer(),
                """{"id":"a","type":"text","sender_device":"PC","created_at":1}""",
            )
            fail("category is required by the frozen protocol")
        } catch (e: kotlinx.serialization.SerializationException) {
            assertTrue(e.message!!.contains("category"))
        }
    }
}
