// Repository ownership changes do not change installed Android package identity.
package io.github.alreadyonapp.alreadyon

import org.junit.Assert.*
import org.junit.Test

class UpdatePolicyTest {
    private val update = AppUpdate("26-10-11-alpha1", "26-10-11-alpha1", 24, "tv",
        "https://github.com/AlreadyOnTV/AlreadyOn/releases/download/26-10-11-alpha1/alreadyon-tv.apk", "a".repeat(64), 1024)
    @Test fun stableInstallsExcludePreviewsAndDrafts() {
        assertFalse(acceptsUpdateRelease("26-10-10-Stable", "26-10-11-alpha1", false, true))
        assertTrue(acceptsUpdateRelease("26-10-10-Stable", "26-10-11-Stable", false, false))
        assertFalse(acceptsUpdateRelease("26-10-10-Stable", "26-10-11-Stable", true, false))
        assertFalse(acceptsUpdateRelease("26-10-10-Stable", "26-10-11-Stable", false, true))
    }
    @Test fun previewInstallsAcceptNewerStagesButIgnoreNonAndroidReleases() {
        for (stage in listOf("alpha2", "RC1", "rc3", "Stable"))
            assertTrue(acceptsUpdateRelease("26-10-10-alpha1", "26-10-11-$stage", false, stage != "Stable"))
        assertFalse(acceptsUpdateRelease("26-10-10-alpha1", "26-10-11", false, false))
    }
    @Test fun shortTagsAcceptHistoricalSignedMetadata() {
        assertTrue(acceptsUpdateRelease("2026-10-18-alpha2", "26-10-18-alpha3", false, true))
        assertTrue(verifiedUpdateMetadata(update.copy(version = "2026-10-11-alpha1"), 23, "tv"))
        assertTrue(validUpdateAsset(update.url.replace("/26-10-11-alpha1/", "/2026-10-11-alpha1/")))
    }
    @Test fun identicalPromotedApksAndDowngradesAreNotUpdates() {
        assertFalse(verifiedUpdateMetadata(update, 24, "tv"))
        assertFalse(verifiedUpdateMetadata(update, 25, "tv"))
        assertTrue(verifiedUpdateMetadata(update, 23, "tv"))
    }
    @Test fun wrongTargetSizeOrHashIsRejected() {
        assertFalse(verifiedUpdateMetadata(update, 23, "phone"))
        assertFalse(verifiedUpdateMetadata(update.copy(size = MAX_UPDATE_BYTES + 1), 23, "tv"))
        assertFalse(verifiedUpdateMetadata(update.copy(size = 0), 23, "tv"))
        assertFalse(verifiedUpdateMetadata(update.copy(hash = "bad"), 23, "tv"))
    }
    @Test fun assetUrlsCannotEscapeRepositoryOrTls() {
        for (url in listOf("http://github.com/AlreadyOnTV/AlreadyOn/releases/download/x/a.apk",
            "https://github.com.evil.example/AlreadyOnTV/AlreadyOn/releases/download/x/a.apk",
            "https://github.com/other/repo/releases/download/x/a.apk",
            "https://user@github.com/AlreadyOnTV/AlreadyOn/releases/download/x/a.apk",
            "https://github.com/AlreadyOnTV/AlreadyOn/releases/download/../a.apk")) assertFalse(validUpdateAsset(url))
        assertTrue(validUpdateAsset(update.url))
    }
}
