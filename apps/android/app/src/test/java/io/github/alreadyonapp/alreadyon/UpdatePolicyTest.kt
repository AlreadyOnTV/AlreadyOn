// Repository ownership changes do not change installed Android package identity.
package io.github.alreadyonapp.alreadyon

import org.junit.Assert.*
import org.junit.Test

class UpdatePolicyTest {
    private val update = AppUpdate("26.10.11-nightly", "26.10.11-nightly", 24, "tv",
        "https://github.com/AlreadyOnTV/AlreadyOn/releases/download/26.10.11-nightly/alreadyon-tv.apk", "a".repeat(64), 1024)
    @Test fun foregroundUpdateChecksThrottleSuccessAndRetryFailuresSooner() {
        assertTrue(updateCheckDue(1L, 0L, lastCheckSucceeded = false))
        assertFalse(updateCheckDue(UPDATE_CHECK_INTERVAL_MS, 1L, lastCheckSucceeded = true))
        assertTrue(updateCheckDue(UPDATE_CHECK_INTERVAL_MS + 1L, 1L, lastCheckSucceeded = true))
        assertFalse(updateCheckDue(UPDATE_CHECK_RETRY_INTERVAL_MS, 1L, lastCheckSucceeded = false))
        assertTrue(updateCheckDue(UPDATE_CHECK_RETRY_INTERVAL_MS + 1L, 1L, lastCheckSucceeded = false))
    }
    @Test fun cycleSelectionAcceptsOnlyNightlyOrStableReleases() {
        for (stage in listOf("nightly", "Stable", "alpha1", "beta2", "RC1")) {
            val prerelease = stage != "Stable"
            assertEquals(stage, stage.lowercase() == "stable" || stage.lowercase() == "nightly",
                acceptsUpdateRelease("26.10.10-RC1", "26.10.11-$stage", false, prerelease, ReleaseCycle.NIGHTLY))
            assertEquals(stage, stage.lowercase() == "stable",
                acceptsUpdateRelease("26.10.10-nightly", "26.10.11-$stage", false, prerelease, ReleaseCycle.STABLE))
            assertFalse(acceptsUpdateRelease("26.10.10-nightly", "26.10.11-$stage", true, prerelease, ReleaseCycle.NIGHTLY))
        }
    }
    @Test fun stableIsDefaultAndInvalidPreferencesFailClosed() {
        assertEquals(ReleaseCycle.STABLE, ReleaseCycle.fromStored(null))
        assertEquals(ReleaseCycle.STABLE, ReleaseCycle.fromStored("unknown"))
        for (cycle in ReleaseCycle.entries) assertEquals(cycle, ReleaseCycle.fromStored(cycle.name))
        assertEquals(ReleaseCycle.STABLE, ReleaseCycle.fromStored("RELEASE_CANDIDATE"))
        assertFalse(acceptsUpdateRelease("26.10.10-nightly", "26.10.11-nightly", false, true))
        assertFalse(acceptsUpdateRelease("26.10.10-Stable", "26.10.11-Stable", false, true))
        assertTrue(acceptsUpdateRelease("26.10.10-nightly", "26.10.11-stable", false, false))
    }
    @Test fun nightliesAcceptNightlyAndStableButRejectLegacyStages() {
        assertTrue(acceptsUpdateRelease("26.10.10-nightly", "26.10.11-nightly", false, true, ReleaseCycle.NIGHTLY))
        assertTrue(acceptsUpdateRelease("26.10.10-nightly", "26.10.11-stable", false, false, ReleaseCycle.NIGHTLY))
        for (stage in listOf("alpha2", "beta1", "rc1"))
            assertFalse(acceptsUpdateRelease("26.10.10-nightly", "26.10.11-$stage", false, true, ReleaseCycle.NIGHTLY))
        assertTrue(validUpdateAsset(update.url.replace("alpha1", "nightly")))
    }
    @Test fun historicalDatePrefixesRemainAcceptedForSupportedStages() {
        assertTrue(acceptsUpdateRelease("2026-10-18-nightly", "26.10.18-nightly", false, true, ReleaseCycle.NIGHTLY))
        assertTrue(verifiedUpdateMetadata(update.copy(version = "2026-10-11-nightly"), 23, "tv"))
        assertTrue(validUpdateAsset(update.url.replace("/26.10.11-nightly/", "/2026-10-11-nightly/")))
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
