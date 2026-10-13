package io.github.alreadyonapp.alreadyon

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import org.junit.Test
import org.junit.runner.RunWith

/** The external emulator driver supplies Android confirmation and verifies replacement after process death. */
@RunWith(AndroidJUnit4::class)
class UpdateInstallationFixture {
    @Test fun stageSyntheticUpdate() = runBlocking {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val context = instrumentation.targetContext
        val store = UpdateInstallStore(context)
        context.getSharedPreferences("synthetic-update-settings", 0).edit().putString("server", "https://synthetic.invalid").commit()
        instrumentation.context.assets.open(if (BuildConfig.IS_TV) "update-tv-test.apk" else "update-phone-test.apk").use { input ->
            store.apk.outputStream().use { input.copyTo(it) }
        }
        val update = AppUpdate("test", "updater-test-24", 24, if (BuildConfig.IS_TV) "tv" else "phone", "", "", store.apk.length())
        verifyUpdateApk(context, store.apk, update)
        stageUpdate(context, update)
        Unit
        // The driver starts the actual app after instrumentation exits; Android stops the
        // instrumented process on completion, which would otherwise race the installer callback.
    }
}
