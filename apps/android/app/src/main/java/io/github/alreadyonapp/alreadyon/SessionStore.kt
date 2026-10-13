package io.github.alreadyonapp.alreadyon

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONArray
import java.security.KeyStore
import java.io.IOException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Stores only origin-bound session cookies; passwords, PINs and media tokens never enter this store. */
internal class SessionStore(context: Context) {
    private val preferences = context.getSharedPreferences("encrypted_session", Context.MODE_PRIVATE)
    private val alias = "alreadyon.session.v1"

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256).build())
        }.generateKey()
    }

    @Synchronized fun read(origin: String): List<String> {
        if (preferences.getString("origin", null) != origin) return emptyList()
        val blob = preferences.getString("data", null) ?: return emptyList()
        return try {
            val parts = blob.split(":")
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, Base64.decode(parts[0], Base64.NO_WRAP)))
            // Authenticated origin binding prevents a stored cookie being moved to a different server.
            cipher.updateAAD(origin.toByteArray(Charsets.UTF_8))
            val data = JSONArray(String(cipher.doFinal(Base64.decode(parts[1], Base64.NO_WRAP)), Charsets.UTF_8))
            List(data.length()) { data.getString(it) }
        } catch (_: Exception) { clear(origin); emptyList() }
    }

    @Synchronized fun write(origin: String, cookies: List<String>) {
        if (cookies.isEmpty()) { clear(origin); return }
        try {
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.ENCRYPT_MODE, key())
            cipher.updateAAD(origin.toByteArray(Charsets.UTF_8))
            val encrypted = cipher.doFinal(JSONArray(cookies).toString().toByteArray(Charsets.UTF_8))
            val blob = Base64.encodeToString(cipher.iv, Base64.NO_WRAP) + ":" + Base64.encodeToString(encrypted, Base64.NO_WRAP)
            // Synchronous commit ensures immediate process termination after login cannot lose the session.
            if (!preferences.edit().putString("origin", origin).putString("data", blob).commit()) throw IOException("Session could not be saved.")
        } catch (_: Exception) { throw IOException("Session could not be saved.") }
    }

    @Synchronized fun clear(origin: String) {
        if (preferences.getString("origin", null) == origin) preferences.edit().clear().commit()
    }
}
