package dev.flowdrop.android.data

/**
 * Raw secret storage used by [SessionVault]. The real implementation encrypts
 * with an Android Keystore AES-GCM key; tests use an in-memory fake.
 */
interface SecureStorage {
    fun read(): String?
    fun write(value: String)
    fun clear()

    /**
     * False once this slot was replaced underneath the handle that holds it
     * (a removed or re-paired record). A store then stops serving its in-memory
     * copy instead of handing out credentials of a pairing that is gone.
     */
    fun isCurrent(): Boolean = true
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
