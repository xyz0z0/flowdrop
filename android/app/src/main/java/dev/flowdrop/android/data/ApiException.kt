package dev.flowdrop.android.data

/** A Hub response error, mirroring the server's `{"error":{"code","message"}}`. */
class ApiException(
    val status: Int,
    val code: String,
    override val message: String,
    cause: Throwable? = null,
) : Exception(message, cause) {
    val isUnauthorized: Boolean get() = status == 401
    val isNetwork: Boolean get() = status == 0
    val isCsrf: Boolean get() = status == 403 && (code == "csrf_missing" || code == "csrf_invalid")
    val isNotInitialized: Boolean get() = code == "not_initialized"
    val isTooLarge: Boolean get() = status == 413 || code == "too_large"
}
