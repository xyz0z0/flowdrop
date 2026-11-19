package dev.flowdrop.android.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class HubEndpointTest {

    @Test
    fun `parses bare host and port as http`() {
        val ep = HubEndpoint.parse("192.168.1.50:8080")
        assertNotNull(ep)
        assertEquals("http://192.168.1.50:8080/", ep!!.baseUrl.toString())
        assertEquals("http://192.168.1.50:8080", ep.origin)
    }

    @Test
    fun `keeps explicit https and default port out of the origin`() {
        val ep = HubEndpoint.parse("https://hub.example")
        assertNotNull(ep)
        assertTrue(ep!!.isHttps)
        assertEquals("https://hub.example", ep.origin)
        assertEquals("https://hub.example/api/v1/events", ep.eventsUrl().toString())
    }

    @Test
    fun `origin includes a non-default port`() {
        val ep = HubEndpoint.parse("http://hub.lan:8080/")
        assertEquals("http://hub.lan:8080", ep!!.origin)
    }

    @Test
    fun `ws url uses ws for http hubs`() {
        val ep = HubEndpoint.parse("http://10.0.2.2:8080")!!
        assertEquals("http://10.0.2.2:8080/api/v1/events", ep.eventsUrl().toString())
    }

    @Test
    fun `rejects a path prefix instead of silently dropping it`() {
        assertNull(HubEndpoint.parse("http://hub.lan:8080/sub/path"))
    }

    @Test
    fun `rejects non http schemes`() {
        assertNull(HubEndpoint.parse("ftp://hub.lan"))
        assertNull(HubEndpoint.parse("file:///etc/passwd"))
    }

    @Test
    fun `rejects blank input`() {
        assertNull(HubEndpoint.parse("   "))
    }

    @Test
    fun `strips trailing slashes and query from the base url`() {
        val ep = HubEndpoint.parse("http://hub.lan:8080/?x=1")!!
        assertEquals("http://hub.lan:8080/", ep.baseUrl.toString())
        assertEquals("http://hub.lan:8080/api/v1/items", ep.url("/api/v1/items").toString())
    }

    @Test
    fun `builds query parameters for pagination`() {
        val ep = HubEndpoint.parse("http://hub.lan:8080")!!
        val url = ep.url("/api/v1/items", mapOf("limit" to "30", "cursor" to "abc"))
        assertEquals("http://hub.lan:8080/api/v1/items?limit=30&cursor=abc", url.toString())
    }
}
