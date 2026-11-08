package dev.flowdrop.android.data

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Encrypts the stored session blob with an AES-256-GCM key that never leaves
 * the Android Keystore, then keeps the ciphertext in app-private preferences.
 *
 * The stored blob contains only cookies, the Hub URL, the username and the
 * device nickname. Passwords are never persisted anywhere.
 */
class KeystoreSecureStorage(context: Context) : SecureStorage {

    private val prefs = context.applicationContext
        .getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)

    override fun read(): String? {
        val encoded = prefs.getString(KEY_BLOB, null) ?: return null
        return try {
            decrypt(encoded)
        } catch (_: Exception) {
            // Key invalidated (e.g. lock-screen reset) or corrupted data:
            // drop it so the user is simply asked to log in again.
            clear()
            null
        }
    }

    override fun write(value: String) {
        val encoded = try {
            encrypt(value)
        } catch (_: Exception) {
            // If the Keystore is unavailable, refuse to keep the session at all
            // rather than writing plaintext cookies to disk.
            prefs.edit().remove(KEY_BLOB).apply()
            return
        }
        prefs.edit().putString(KEY_BLOB, encoded).apply()
    }

    override fun clear() {
        prefs.edit().remove(KEY_BLOB).apply()
    }

    private fun key(): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (keyStore.getEntry(KEY_ALIAS, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    private fun encrypt(plain: String): String {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val iv = cipher.iv
        val cipherText = cipher.doFinal(plain.toByteArray(Charsets.UTF_8))
        val combined = ByteArray(1 + iv.size + cipherText.size)
        combined[0] = iv.size.toByte()
        iv.copyInto(combined, destinationOffset = 1)
        cipherText.copyInto(combined, destinationOffset = 1 + iv.size)
        return Base64.encodeToString(combined, Base64.NO_WRAP)
    }

    private fun decrypt(encoded: String): String? {
        val combined = Base64.decode(encoded, Base64.NO_WRAP)
        if (combined.size < 2) return null
        val ivSize = combined[0].toInt()
        if (ivSize <= 0 || 1 + ivSize >= combined.size) return null
        val iv = combined.copyOfRange(1, 1 + ivSize)
        val cipherText = combined.copyOfRange(1 + ivSize, combined.size)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, iv))
        return String(cipher.doFinal(cipherText), Charsets.UTF_8)
    }

    private companion object {
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "flowdrop.session.v1"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val PREFS_NAME = "flowdrop.session"
        const val KEY_BLOB = "blob"
    }
}
