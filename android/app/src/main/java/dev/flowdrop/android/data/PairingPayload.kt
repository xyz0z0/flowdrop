package dev.flowdrop.android.data

import java.io.ByteArrayOutputStream

/**
 * What a FlowDrop pairing QR code carries.
 *
 * The QR code is only a typing shortcut for the four-digit code shown on the
 * computer; it never replaces `POST /api/v1/auth/pair` and never contains a
 * device token. The payload is as sensitive as the code on screen, so it is
 * parsed in memory and never logged or persisted.
 *
 * `flowdrop://pair?v=1&code=DDDD[&url=<percent-encoded absolute http(s) URL>]`
 */
data class PairingPayload(
    val code: String,
    /** Candidate Hub addresses, in payload order, de-duplicated, non-loopback. */
    val hubUrls: List<String>,
)

/**
 * Which login fields a scanned code fills in.
 *
 * [hubAddress] stays null when the user already typed an address: scanning must
 * never overwrite manual input. A null code is impossible: an invalid code
 * invalidates the whole payload (see [parsePairingPayload]).
 */
data class PairingPrefill(
    val code: String,
    val hubAddress: String?,
)

/**
 * Parses a pairing QR payload, or returns null when the whole payload is
 * unusable. Unknown parameters are ignored and an unusable single `url` is
 * dropped instead of invalidating the code.
 */
fun parsePairingPayload(raw: String): PairingPayload? {
    val text = raw.trim()
    if (text.isEmpty()) return null

    val schemeEnd = text.indexOf(':')
    if (schemeEnd <= 0) return null
    if (!text.substring(0, schemeEnd).equals(PAIR_SCHEME, ignoreCase = true)) return null

    val afterScheme = text.substring(schemeEnd + 1)
    if (!afterScheme.startsWith("//")) return null

    val afterSlashes = afterScheme.substring(2)
    val authorityEnd = afterSlashes.indexOfFirst { it == '?' || it == '#' || it == '/' }
    val authority = if (authorityEnd < 0) afterSlashes else afterSlashes.substring(0, authorityEnd)
    if (!authority.equals(PAIR_HOST, ignoreCase = true)) return null

    val queryStart = afterSlashes.indexOf('?')
    val query = if (queryStart < 0) {
        ""
    } else {
        afterSlashes.substring(queryStart + 1).substringBefore('#')
    }

    var version: String? = null
    var code: String? = null
    val urls = mutableListOf<String>()

    for (param in query.split('&')) {
        if (param.isEmpty()) continue
        val name = param.substringBefore('=')
        val rawValue = if (param.contains('=')) param.substringAfter('=') else ""
        val value = percentDecode(rawValue) ?: continue
        when (name) {
            "v" -> version = value
            "code" -> code = value
            "url" -> {
                val candidate = value.trim()
                if (isAcceptableHubUrl(candidate) && candidate !in urls) urls += candidate
            }
            // Unknown parameters are ignored on purpose: the payload may grow.
        }
    }

    if (version != PAIR_VERSION) return null
    val pairingCode = code ?: return null
    if (pairingCode.length != PAIR_CODE_LENGTH) return null
    if (!pairingCode.all { it in '0'..'9' }) return null

    return PairingPayload(code = pairingCode, hubUrls = urls)
}

/**
 * Scanned payloads only prefill; the user still confirms the address and taps
 * the pairing button themselves.
 */
fun pairingPrefill(payload: PairingPayload, currentHubAddress: String): PairingPrefill =
    PairingPrefill(
        code = payload.code,
        hubAddress = if (currentHubAddress.isBlank()) payload.hubUrls.firstOrNull() else null,
    )

/**
 * True for an absolute `http`/`https` URL the phone can plausibly reach.
 *
 * Loopback and unspecified addresses are rejected: they would only ever name
 * the phone itself, and the desktop is required to keep them out of the QR.
 * Deliberately hand-rolled so the parser is plain JVM code with no
 * `android.net.Uri` (the Hub stores no dependency on Android for parsing).
 */
internal fun isAcceptableHubUrl(raw: String): Boolean {
    if (raw.isEmpty()) return false
    if (raw.any { it.isWhitespace() }) return false

    val match = ABSOLUTE_HTTP_URL.matchEntire(raw) ?: return false
    val scheme = match.groupValues[1].lowercase()
    if (scheme != "http" && scheme != "https") return false

    val authority = match.groupValues[2]
    if (authority.isEmpty()) return false
    // Userinfo would smuggle credentials into a QR code; never accepted.
    if (authority.contains('@')) return false

    val host: String
    val port: String?
    if (authority.startsWith("[")) {
        val close = authority.indexOf(']')
        if (close < 0) return false
        host = authority.substring(1, close)
        val remainder = authority.substring(close + 1)
        port = when {
            remainder.isEmpty() -> null
            remainder.startsWith(":") -> remainder.substring(1)
            else -> return false
        }
    } else {
        val colon = authority.lastIndexOf(':')
        if (colon >= 0) {
            host = authority.substring(0, colon)
            port = authority.substring(colon + 1)
        } else {
            host = authority
            port = null
        }
    }
    if (host.isEmpty()) return false

    if (port != null) {
        if (port.isEmpty() || port.length > 5) return false
        if (!port.all { it in '0'..'9' }) return false
        val value = port.toIntOrNull() ?: return false
        if (value !in 1..65535) return false
    }

    return !isLoopbackHost(host)
}

/** localhost, 127.0.0.0/8, ::1, 0.0.0.0 and :: name the phone, not the Hub. */
internal fun isLoopbackHost(host: String): Boolean {
    val name = host.lowercase()
    if (name == "localhost" || name.endsWith(".localhost")) return true

    if (name.contains(':')) {
        return when (name) {
            "::1", "::" -> true
            "0:0:0:0:0:0:0:1", "0:0:0:0:0:0:0:0" -> true
            else -> false
        }
    }

    val octets = name.split('.')
    if (octets.size != 4) return false
    val numbers = IntArray(4)
    for (index in octets.indices) {
        val octet = octets[index]
        if (octet.isEmpty() || octet.length > 3) return false
        if (!octet.all { it in '0'..'9' }) return false
        val value = octet.toIntOrNull() ?: return false
        if (value !in 0..255) return false
        numbers[index] = value
    }
    if (numbers[0] == 127) return true
    return numbers.all { it == 0 }
}

private fun percentDecode(raw: String): String? {
    if (raw.indexOf('%') < 0) return raw
    val out = ByteArrayOutputStream(raw.length)
    var index = 0
    while (index < raw.length) {
        val char = raw[index]
        if (char == '%') {
            if (index + 2 >= raw.length) return null
            val high = hexValue(raw[index + 1])
            val low = hexValue(raw[index + 2])
            if (high < 0 || low < 0) return null
            out.write((high shl 4) or low)
            index += 3
        } else {
            // Non-ASCII characters (and '+' which is a literal plus in a URL
            // value) pass through as UTF-8 bytes.
            out.write(char.toString().toByteArray(Charsets.UTF_8))
            index += 1
        }
    }
    return String(out.toByteArray(), Charsets.UTF_8)
}

private fun hexValue(char: Char): Int = when (char) {
    in '0'..'9' -> char - '0'
    in 'a'..'f' -> char - 'a' + 10
    in 'A'..'F' -> char - 'A' + 10
    else -> -1
}

private const val PAIR_SCHEME = "flowdrop"
private const val PAIR_HOST = "pair"
private const val PAIR_VERSION = "1"
private const val PAIR_CODE_LENGTH = 4

private val ABSOLUTE_HTTP_URL = Regex("^([A-Za-z][A-Za-z0-9+.\\-]*)://([^/?#]*)([/?#].*)?$")
