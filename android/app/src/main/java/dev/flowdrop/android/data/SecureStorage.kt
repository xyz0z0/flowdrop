package dev.flowdrop.android.data

/**
 * Raw secret storage used by [SessionVault]. The real implementation encrypts
 * with an Android Keystore AES-GCM key; tests use an in-memory fake.
 */
interface SecureStorage {
    fun read(): String?
    fun write(value: String)
    fun clear()
}

class InMemorySecureStorage(initial: String? = null) : SecureStorage {
    private var value: String? = initial
    override fun read(): String? = value
    override fun write(value: String) {
        this.value = value
    }

    override fun clear() {
        value = null
    }
}
