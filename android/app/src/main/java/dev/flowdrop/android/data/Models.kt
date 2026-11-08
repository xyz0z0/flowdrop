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
    val text: String? = null,
    @SerialName("sender_device") val senderDevice: String = "",
    @SerialName("created_at") val createdAt: Long = 0,
    val blob: Blob? = null,
) {
    val isImage: Boolean get() = type == "image" && blob != null
    val isLink: Boolean get() = type == "link"
    val isApk: Boolean get() = blob != null && blob.name.endsWith(".apk", ignoreCase = true)
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

@Serializable
data class SessionInfo(
    val authenticated: Boolean = false,
    @SerialName("csrf_token") val csrfToken: String? = null,
    val username: String? = null,
    @SerialName("expires_at") val expiresAt: Long? = null,
    val limits: SessionLimits? = null,
)

@Serializable
data class LoginRequest(
    val username: String,
    val password: String,
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
