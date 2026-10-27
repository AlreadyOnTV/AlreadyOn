// YY.MM.DD releases retain the installed package identity; read old signed versions too.
package io.github.alreadyonapp.alreadyon

import java.net.URI

internal const val UPDATE_PACKAGE = "io.github.alreadyonapp.alreadyon"
internal const val UPDATE_REPOSITORY = "AlreadyOnTV/AlreadyOn"
internal const val MAX_UPDATE_BYTES = 256L * 1024 * 1024
internal data class AppUpdate(val release: String, val version: String, val code: Long,
    val target: String, val url: String, val hash: String, val size: Long)

internal fun acceptsUpdateRelease(installed: String, tag: String, draft: Boolean, prerelease: Boolean): Boolean {
    if (draft || !Regex("(?:\\d{2}\\.\\d{2}\\.\\d{2}|(?:20)?\\d{2}-\\d{2}-\\d{2})-(nightly|alpha\\d+|beta\\d*|rc\\d+|stable)", RegexOption.IGNORE_CASE).matches(tag)) return false
    val stable = installed.endsWith("-stable", ignoreCase = true)
    return !stable || (!prerelease && tag.endsWith("-stable", ignoreCase = true))
}

/** Initial asset URLs must name this public repository; redirect hosts are checked separately. */
internal fun validUpdateAsset(url: String): Boolean {
    val uri = runCatching { URI(url) }.getOrNull() ?: return false
    return uri.scheme == "https" && uri.host == "github.com" && uri.userInfo == null &&
        uri.port == -1 && uri.rawQuery == null && uri.fragment == null &&
        Regex("/AlreadyOnTV/AlreadyOn/releases/download/(?:\\d{2}\\.\\d{2}\\.\\d{2}|(?:20)?\\d{2}-\\d{2}-\\d{2})-(nightly|alpha\\d+|beta\\d*|rc\\d+|stable)/(android-release\\.json|alreadyon-android\\.apk|alreadyon-tv\\.apk)", RegexOption.IGNORE_CASE).matches(uri.rawPath)
}

internal fun verifiedUpdateMetadata(update: AppUpdate, installedCode: Long, target: String): Boolean =
    update.code > installedCode && update.target == target && update.size in 1..MAX_UPDATE_BYTES &&
        Regex("[a-fA-F0-9]{64}").matches(update.hash) && validUpdateAsset(update.url)

/** Compare release stages numerically; RC10 follows RC9, regardless of tag casing. */
internal fun releaseOrder(label: String): List<Int>? {
    val match = Regex("(\\d{2}\\.\\d{2}\\.\\d{2}|(?:20)?\\d{2}-\\d{2}-\\d{2})-(nightly|alpha|beta|rc|stable)(\\d*)", RegexOption.IGNORE_CASE).matchEntire(label) ?: return null
    val date = match.groupValues[1].replace(".", "-").split("-").map { it.toInt() }
    return listOf(if (date[0] < 100) date[0] + 2000 else date[0], date[1], date[2],
        listOf("nightly", "alpha", "beta", "rc", "stable").indexOf(match.groupValues[2].lowercase()),
        match.groupValues[3].toIntOrNull() ?: 0)
}
internal fun newerRelease(candidate: String, current: String): Boolean {
    val next = releaseOrder(candidate) ?: return false
    val previous = releaseOrder(current) ?: return false
    return next.zip(previous).firstOrNull { it.first != it.second }?.let { it.first > it.second } ?: false
}

/** Same-code promotions must name the exact installed APK, never a replacement build. */
internal fun eligibleUpdate(update: AppUpdate, code: Long, target: String, release: String, installedHash: String?): Boolean =
    verifiedUpdateMetadata(update, code - 1, target) &&
        (update.code > code || (update.code == code && installedHash != null &&
            update.hash.equals(installedHash, true) && newerRelease(update.release, release)))
