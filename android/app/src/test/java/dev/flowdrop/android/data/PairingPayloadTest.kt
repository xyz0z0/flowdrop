package dev.flowdrop.android.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pins the pairing-QR payload contract from docs/PAIRING.md:
 *
 * `flowdrop://pair?v=1&code=DDDD[&url=...]`
 *
 * Unknown parameters are ignored, an unusable single `url` is dropped while the
 * code stays valid, and anything wrong with the scheme/host/version/code
 * invalidates the whole payload.
 */
class PairingPayloadTest {

    private fun parse(raw: String): PairingPayload? = parsePairingPayload(raw)

    @Test
    fun `parses a minimal payload`() {
        val payload = parse("flowdrop://pair?v=1&code=0429")
        assertEquals(PairingPayload(code = "0429", hubUrls = emptyList()), payload)
    }

    @Test
    fun `keeps leading zeros in the code`() {
        assertEquals("0007", parse("flowdrop://pair?v=1&code=0007")?.code)
        assertEquals("0100", parse("flowdrop://pair?v=1&code=0100")?.code)
    }

    @Test
    fun `accepts a full payload with repeated urls`() {
        val payload = parse(
            "flowdrop://pair?v=1&code=1234" +
                "&url=http%3A%2F%2F192.168.1.10%3A8080" +
                "&url=https%3A%2F%2Fhub.tailnet.ts.net" +
                "&url=http%3A%2F%2F192.168.1.10%3A8080",
        )
        assertNotNull(payload)
        assertEquals("1234", payload!!.code)
        assertEquals(
            listOf("http://192.168.1.10:8080", "https://hub.tailnet.ts.net"),
            payload.hubUrls,
        )
    }

    @Test
    fun `keeps candidate order across distinct addresses`() {
        val payload = parse(
            "flowdrop://pair?v=1&code=1234&url=http%3A%2F%2F10.0.0.2%3A8080" +
                "&url=http%3A%2F%2F10.0.0.1%3A8080",
        )
        assertEquals(
            listOf("http://10.0.0.2:8080", "http://10.0.0.1:8080"),
            payload?.hubUrls,
        )
    }

    @Test
    fun `scheme and host are case insensitive`() {
        assertEquals("0429", parse("FLOWDROP://Pair?v=1&code=0429")?.code)
    }

    @Test
    fun `rejects a missing code`() {
        assertNull(parse("flowdrop://pair?v=1"))
        assertNull(parse("flowdrop://pair?v=1&url=http%3A%2F%2F192.168.1.10%3A8080"))
        assertNull(parse("flowdrop://pair?code=0429"))
    }

    @Test
    fun `rejects a code that is not four digits`() {
        assertNull(parse("flowdrop://pair?v=1&code=123"))
        assertNull(parse("flowdrop://pair?v=1&code=12345"))
        assertNull(parse("flowdrop://pair?v=1&code=12a4"))
        assertNull(parse("flowdrop://pair?v=1&code="))
        // Full-width digits are not ASCII digits.
        assertNull(parse("flowdrop://pair?v=1&code=%EF%BC%90%EF%BC%91%EF%BC%92%EF%BC%93"))
    }

    @Test
    fun `rejects a missing or different version`() {
        assertNull(parse("flowdrop://pair?code=0429"))
        assertNull(parse("flowdrop://pair?v=2&code=0429"))
        assertNull(parse("flowdrop://pair?v=&code=0429"))
    }

    @Test
    fun `rejects a foreign scheme or host`() {
        assertNull(parse("flowdrop2://pair?v=1&code=0429"))
        assertNull(parse("http://pair?v=1&code=0429"))
        assertNull(parse("flowdrop://paired?v=1&code=0429"))
        assertNull(parse("flowdrop://pair.example.com?v=1&code=0429"))
        assertNull(parse("flowdrop://pair.example.com:8080?v=1&code=0429"))
        assertNull(parse("flowdrop:/pair?v=1&code=0429"))
        assertNull(parse("pair?v=1&code=0429"))
        assertNull(parse(""))
        assertNull(parse("   "))
    }

    @Test
    fun `drops non http urls but keeps the code`() {
        val payload = parse(
            "flowdrop://pair?v=1&code=0429" +
                "&url=ftp%3A%2F%2F192.168.1.10" +
                "&url=javascript%3Aalert(1)" +
                "&url=file%3A%2F%2F%2Fetc%2Fpasswd" +
                "&url=192.168.1.10%3A8080",
        )
        assertNotNull(payload)
        assertEquals("0429", payload!!.code)
        assertTrue(payload.hubUrls.isEmpty())
    }

    @Test
    fun `drops loopback and unspecified urls`() {
        val payload = parse(
            "flowdrop://pair?v=1&code=0429" +
                "&url=http%3A%2F%2Flocalhost%3A8080" +
                "&url=http%3A%2F%2Flocalhost" +
                "&url=http%3A%2F%2F127.0.0.1%3A8080" +
                "&url=http%3A%2F%2F127.10.1.2" +
                "&url=http%3A%2F%2F%5B%3A%3A1%5D%3A8080" +
                "&url=http%3A%2F%2F0.0.0.0%3A8080" +
                "&url=http%3A%2F%2F%5B%3A%3A%5D%3A8080" +
                "&url=http%3A%2F%2F192.168.1.10%3A8080",
        )
        assertNotNull(payload)
        assertEquals(listOf("http://192.168.1.10:8080"), payload!!.hubUrls)
    }

    @Test
    fun `rejects urls with credentials or bad ports`() {
        val payload = parse(
            "flowdrop://pair?v=1&code=0429" +
                "&url=http%3A%2F%2Fuser%3Apass%40192.168.1.10%3A8080" +
                "&url=http%3A%2F%2F192.168.1.10%3A0" +
                "&url=http%3A%2F%2F192.168.1.10%3A70000" +
                "&url=http%3A%2F%2F192.168.1.10%3A80x",
        )
        assertTrue(payload?.hubUrls?.isEmpty() == true)
    }

    @Test
    fun `ignores unknown parameters`() {
        val payload = parse(
            "flowdrop://pair?v=1&code=0429&device=phone&exp=1700000000&note=%E4%BD%A0%E5%A5%BD",
        )
        assertEquals(PairingPayload(code = "0429", hubUrls = emptyList()), payload)
    }

    @Test
    fun `drops a url with broken percent encoding`() {
        val payload = parse(
            "flowdrop://pair?v=1&code=0429&url=http%3A%2F%2F192.168.1.10%ZZ",
        )
        assertEquals(PairingPayload(code = "0429", hubUrls = emptyList()), payload)
    }

    @Test
    fun `prefill keeps a manually typed address and only fills the code`() {
        val payload = parse("flowdrop://pair?v=1&code=0429&url=http%3A%2F%2F192.168.1.10%3A8080")!!
        assertEquals(
            PairingPrefill(code = "0429", hubAddress = null),
            pairingPrefill(payload, currentHubAddress = "http://10.0.0.9:8080"),
        )
        // A whitespace-only field counts as empty, so the candidate is offered.
        assertEquals(
            PairingPrefill(code = "0429", hubAddress = "http://192.168.1.10:8080"),
            pairingPrefill(payload, currentHubAddress = "   "),
        )
    }

    @Test
    fun `prefill uses the first candidate when the address is empty`() {
        val payload = parse(
            "flowdrop://pair?v=1&code=0429" +
                "&url=http%3A%2F%2F192.168.1.10%3A8080" +
                "&url=https%3A%2F%2Fhub.tailnet.ts.net",
        )!!
        assertEquals(
            PairingPrefill(code = "0429", hubAddress = "http://192.168.1.10:8080"),
            pairingPrefill(payload, currentHubAddress = ""),
        )
    }

    @Test
    fun `prefill without candidates leaves the address alone`() {
        val payload = parse("flowdrop://pair?v=1&code=0429")!!
        assertEquals(
            PairingPrefill(code = "0429", hubAddress = null),
            pairingPrefill(payload, currentHubAddress = ""),
        )
    }

    @Test
    fun `url validation accepts private and tailscale style hosts`() {
        assertTrue(isAcceptableHubUrl("http://192.168.1.10:8080"))
        assertTrue(isAcceptableHubUrl("https://hub.tailnet.ts.net"))
        assertTrue(isAcceptableHubUrl("http://hub.lan:8080/"))
        assertTrue(isAcceptableHubUrl("http://100.101.102.103:8080"))
        assertTrue(isAcceptableHubUrl("http://[fd7a:115c:a1e0::1]:8080"))
    }
}
