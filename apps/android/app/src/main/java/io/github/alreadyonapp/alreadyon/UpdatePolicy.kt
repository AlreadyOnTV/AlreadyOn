// YY.MM.DD releases retain the installed package identity; read old signed versions too.
package io.github.alreadyonapp.alreadyon

import java.net.URI

internal const val UPDATE_PACKAGE = "io.github.alreadyonapp.alreadyon"
internal const val UPDATE_REPOSITORY = "AlreadyOnTV/AlreadyOn"
internal const val MAX_UPDATE_BYTES = 256L * 1024 * 1024
internal data class AppUpdate(val release: String, val version: String, val code: Long,
    val target: String, val url: String, val hash: String, val size: Long)

internal enum class ReleaseCycle(val label: String) {
    STABLE("Stable"), NIGHTLY("Nightly");

    companion object {
        fun fromStored(value: String?): ReleaseCycle = entries.firstOrNull { it.name == value } ?: STABLE
    }
}

internal const val UPDATE_CHECK_INTERVAL_MS = 15 * 60 * 1000L
internal const val UPDATE_CHECK_RETRY_INTERVAL_MS = 60 * 1000L
internal fun updateCheckDue(nowElapsedMs: Long, lastCheckElapsedMs: Long, lastCheckSucceeded: Boolean): Boolean {
    val interval = if (lastCheckSucceeded) UPDATE_CHECK_INTERVAL_MS else UPDATE_CHECK_RETRY_INTERVAL_MS
    return lastCheckElapsedMs <= 0 || nowElapsedMs - lastCheckElapsedMs >= interval
}

internal fun acceptsUpdateRelease(installed: String, tag: String, draft: Boolean, prerelease: Boolean,
    cycle: ReleaseCycle = ReleaseCycle.STABLE): Boolean {
    if (draft) return false
    val stage = Regex("(?:\\d{2}\\.\\d{2}\\.\\d{2}|(?:20)?\\d{2}-\\d{2}-\\d{2})-(nightly|stable)", RegexOption.IGNORE_CASE)
        .matchEntire(tag)?.groupValues?.get(1)?.lowercase() ?: return false
    return when (cycle) {
        ReleaseCycle.STABLE -> stage == "stable" && !prerelease
        ReleaseCycle.NIGHTLY -> stage == "nightly" || (stage == "stable" && !prerelease)
    }
}

/** Initial asset URLs must name this public repository; redirect hosts are checked separately. */
internal fun validUpdateAsset(url: String): Boolean {
    val uri = runCatching { URI(url) }.getOrNull() ?: return false
    return uri.scheme == "https" && uri.host == "github.com" && uri.userInfo == null &&
        uri.port == -1 && uri.rawQuery == null && uri.fragment == null &&
        Regex("/AlreadyOnTV/AlreadyOn/releases/download/(?:\\d{2}\\.\\d{2}\\.\\d{2}|(?:20)?\\d{2}-\\d{2}-\\d{2})-(nightly|stable)/(android-release\\.json|alreadyon-android\\.apk|alreadyon-tv\\.apk)", RegexOption.IGNORE_CASE).matches(uri.rawPath)
}

internal fun verifiedUpdateMetadata(update: AppUpdate, installedCode: Long, target: String): Boolean =
    update.code > installedCode && update.target == target && update.size in 1..MAX_UPDATE_BYTES &&
        Regex("[a-fA-F0-9]{64}").matches(update.hash) && validUpdateAsset(update.url)
