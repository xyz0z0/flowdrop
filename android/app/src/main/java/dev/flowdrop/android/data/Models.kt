package dev.flowdrop.android.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Mirrors the Hub JSON contract in web/src/api.ts and server/internal/store. */
@Serializable
data class Blob(
    val id: String,
    val name: String,
    @SerialName("content_type") val contentType: String = "application/octet-stream",
    @SerialName("size_bytes") val sizeBytes: Long = 0,
    val sha256: String = "",
    @SerialName("created_at") val createdAt: Long = 0,
)

@Serializable
data class Item(
    val id: String,
    val type: String,
    /**
     * Server-assigned file category (`text|link|image|video|audio|document|archive|other`).
     * Required and never derived on the client: the Hub is the single source.
     */
    val category: String,
    val text: String? = null,
    @SerialName("sender_device") val senderDevice: String = "",
    @SerialName("created_at") val createdAt: Long = 0,
    val blob: Blob? = null,
) {
    val isImage: Boolean get() = type == "image" && blob != null
    val isLink: Boolean get() = type == "link"
    val isApk: Boolean get() = blob != null && blob.name.endsWith(".apk", ignoreCase = true)
}

/**
 * Filter values of `GET /api/v1/items?category=`. [ALL] means "no filter"; every
 * other value is decided by the Hub, so the client never classifies an item.
 */
object ItemCategory {
    const val ALL = "all"
    const val TEXT = "text"
    const val LINK = "link"
    const val IMAGE = "image"
    const val VIDEO = "video"
    const val AUDIO = "audio"
    const val DOCUMENT = "document"
    const val ARCHIVE = "archive"
    const val OTHER = "other"

    /** Chip order, identical to the web client. */
    val filters: List<String> = listOf(ALL, TEXT, LINK, IMAGE, VIDEO, AUDIO, DOCUMENT, ARCHIVE, OTHER)

    /** An item is visible when no filter is active or when the Hub said it matches. */
    fun matches(item: Item, filter: String): Boolean = filter == ALL || item.category == filter
}

@Serializable
data class Page(
    val items: List<Item> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String = "",
)

@Serializable
data class SessionLimits(
    @SerialName("max_upload_bytes") val maxUploadBytes: Long = DEFAULT_MAX_UPLOAD_BYTES,
)

/**
 * One stored address of a paired Hub, exactly as the frozen `hub` payload
 * carries it (`kind` is `lan`, `tailscale` or `other`).
 *
 * This is the serialisable form of [RouteCandidate]: the selector's data class
 * is intentionally not `@Serializable`, so the store keeps endpoints and the
 * integration layer maps them for [HubRouteSelector].
 */
@Serializable
data class HubEndpointInfo(
    val url: String,
    val kind: String = RouteCandidate.KIND_OTHER,
) {
    fun toRouteCandidate(): RouteCandidate = RouteCandidate(url = url, kind = kind)
}

@Serializable
data class HubInfo(
    val id: String = "",
    val name: String = "",
    val endpoints: List<HubEndpointInfo> = emptyList(),
)

/** Public response of GET /api/v1/health; `hub_id` is the credential-free identity. */
@Serializable
data class HealthInfo(
    @SerialName("hub_id") val hubId: String = "",
    val status: String = "",
    val service: String = "",
    val version: String = "",
    val time: String = "",
)

@Serializable
data class PairRequest(
    val code: String,
    @SerialName("device_name") val deviceName: String,
    val native: Boolean = true,
)

@Serializable
data class SessionInfo(
    val authenticated: Boolean = false,
    @SerialName("csrf_token") val csrfToken: String? = null,
    @SerialName("expires_at") val expiresAt: Long? = null,
    val limits: SessionLimits? = null,
    val hub: HubInfo? = null,
    @SerialName("device_id") val deviceId: String? = null,
    // Only returned once, by POST /auth/pair with native=true. GET /auth/session
    // deliberately never echoes it, so a missing value must not erase the stored one.
    @SerialName("device_token") val deviceToken: String? = null,
)

@Serializable
data class CreateTextRequest(
    val type: String,
    val text: String,
    @SerialName("sender_device") val senderDevice: String,
)

@Serializable
data class ErrorBody(
    val error: ErrorDetail? = null,
)

@Serializable
data class ErrorDetail(
    val code: String = "",
    val message: String = "",
)

@Serializable
internal data class EventMessage(
    val type: String = "",
    val id: String = "",
)

const val DEFAULT_MAX_UPLOAD_BYTES: Long = 512L * 1024 * 1024
