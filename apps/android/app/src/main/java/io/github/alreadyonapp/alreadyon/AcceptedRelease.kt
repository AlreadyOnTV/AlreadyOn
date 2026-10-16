// Accepted release labels are bound to the installed APK independently of temporary installer state.
package io.github.alreadyonapp.alreadyon

import android.content.Context
import java.io.File
import java.security.MessageDigest

internal fun installedApkHash(context: Context): String {
    val digest = MessageDigest.getInstance("SHA-256")
    File(context.applicationInfo.sourceDir).inputStream().use { input ->
        val buffer = ByteArray(64 * 1024)
        while (true) {
            val count = input.read(buffer)
            if (count < 0) break
            digest.update(buffer, 0, count)
        }
    }
    return digest.digest().joinToString("") { "%02x".format(it) }
}

/** Separate from temporary installer state, which cancellation and cleanup may clear. */
internal class AcceptedRelease(private val context: Context) {
    private val preferences = context.getSharedPreferences("accepted-release", Context.MODE_PRIVATE)
    fun current(hash: String): String {
        val embedded = context.packageManager.getPackageInfo(context.packageName, 0).versionName.orEmpty()
        val label = preferences.getString("release", null) ?: return embedded
        return if (preferences.getLong("code", -1) == installedVersionCode(context) &&
            preferences.getString("hash", null).equals(hash, true) && newerRelease(label, embedded)) label else embedded
    }
    fun accept(update: AppUpdate) {
        val hash = installedApkHash(context)
        check(update.code == installedVersionCode(context) && update.hash.equals(hash, true)) {
            "The installed APK does not match this release."
        }
        check(newerRelease(update.release, current(hash))) { "This release is not newer than the current release." }
        check(preferences.edit().putString("release", update.release).putLong("code", update.code)
            .putString("hash", hash).commit()) { "Could not save the release version." }
    }
}
