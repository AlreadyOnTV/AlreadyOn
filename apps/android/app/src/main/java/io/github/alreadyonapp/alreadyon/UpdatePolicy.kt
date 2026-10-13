package io.github.alreadyonapp.alreadyon

import java.net.URI

internal const val UPDATE_PACKAGE = "io.github.alreadyonapp.alreadyon"
internal const val UPDATE_REPOSITORY = "AlreadyOnApp/AlreadyOn"
internal const val MAX_UPDATE_BYTES = 256L * 1024 * 1024
internal data class AppUpdate(val release: String, val version: String, val code: Long,
    val target: String, val url: String, val hash: String, val size: Long)

internal fun acceptsUpdateRelease(installed: String, tag: String, draft: Boolean, prerelease: Boolean): Boolean {
    if (draft || !Regex("\\d{4}-\\d{2}-\\d{2}-(alpha\\d+|beta\\d*|rc\\d+|stable)", RegexOption.IGNORE_CASE).matches(tag)) return false
    val stable = installed.endsWith("-stable", ignoreCase = true)
    return !stable || (!prerelease && tag.endsWith("-stable", ignoreCase = true))
}

/** Initial asset URLs must name this public repository; redirect hosts are checked separately. */
internal fun validUpdateAsset(url: String): Boolean {
    val uri = runCatching { URI(url) }.getOrNull() ?: return false
    return uri.scheme == "https" && uri.host == "github.com" && uri.userInfo == null &&
        uri.port == -1 && uri.rawQuery == null && uri.fragment == null &&
        Regex("/AlreadyOnApp/AlreadyOn/releases/download/\\d{4}-\\d{2}-\\d{2}-(alpha\\d+|beta\\d*|rc\\d+|stable)/(android-release\\.json|alreadyon-android\\.apk|alreadyon-tv\\.apk)", RegexOption.IGNORE_CASE).matches(uri.rawPath)
}

internal fun verifiedUpdateMetadata(update: AppUpdate, installedCode: Long, target: String): Boolean =
    update.code > installedCode && update.target == target && update.size in 1..MAX_UPDATE_BYTES &&
        Regex("[a-fA-F0-9]{64}").matches(update.hash) && validUpdateAsset(update.url)
