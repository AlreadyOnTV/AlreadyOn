// Synthetic preferences and opt-in disposable server only; never use a normal app's account.
package io.github.alreadyonapp.alreadyon

import android.app.Application
import android.content.Context
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Surface
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.unit.dp
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.UiDevice
import kotlinx.coroutines.runBlocking
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.util.Base64

@RunWith(AndroidJUnit4::class)
class AdminSettingsTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()
    private class FixtureApplication(context: Context) : Application() {
        init { attachBaseContext(context) }
        override fun getSharedPreferences(name: String, mode: Int) = super.getSharedPreferences("settings-102-$name", mode)
    }
    private val application get() = FixtureApplication(InstrumentationRegistry.getInstrumentation().targetContext)
    private fun settled(model: PhoneModel) {
        compose.waitForIdle()
        compose.waitUntil(30_000) { !model.state.value.busy }
        compose.waitForIdle()
    }

    @Test fun adminMenuOrderingAndViewerGuards() {
        val application = application
        application.getSharedPreferences("app-updates", 0).edit().clear().commit()
        val store = ViewModelStore()
        lateinit var model: PhoneModel
        var state by mutableStateOf(PhoneState(signedIn = true, role = "owner",
            profile = ViewerProfile(1, "Viewer Profile", "adult", false, "en", null)))
        try {
            compose.runOnUiThread {
                model = ViewModelProvider(store, ViewModelProvider.AndroidViewModelFactory(application))[PhoneModel::class.java]
                compose.activity.setContent { PhoneTheme { ProfileMenu(state, model) } }
            }
            settled(model)
            compose.onNodeWithText("Viewer Profile").performClick()
            compose.onNodeWithText("Settings").assertDoesNotExist()
            compose.onNodeWithText("Sign Out").assertIsDisplayed()
            compose.runOnIdle { state = state.copy(profile = state.profile!!.copy(role = "admin")) }
            val settings = compose.onNodeWithText("Settings").fetchSemanticsNode().boundsInRoot
            val signOut = compose.onNodeWithText("Sign Out").fetchSemanticsNode().boundsInRoot
            assertTrue("Settings is immediately before Sign Out", settings.bottom <= signOut.top)
            compose.runOnUiThread {
                model.openSettings("admin")
                model.setReleaseCycle(ReleaseCycle.NIGHTLY)
                model.deleteManagedProfile(999)
            }
            settled(model)
            assertNull(model.state.value.settings)
            assertEquals(ReleaseCycle.STABLE, model.releaseCycle.value)
            assertFalse(application.getSharedPreferences("app-updates", 0).contains("release_cycle"))
            assertTrue(model.state.value.error.orEmpty().contains("Admin profile"))
        } finally { compose.runOnUiThread { store.clear() } }
    }

    @Test fun currentAccountManagementAndCyclePersistence() {
        val arguments = InstrumentationRegistry.getArguments()
        assumeTrue("Requires an isolated staged server fixture", arguments.getString("settingsOrigin") != null)
        val origin = requireNotNull(arguments.getString("settingsOrigin"))
        require(origin == "https://127.0.0.1:19950")
        val ca = String(Base64.getDecoder().decode(requireNotNull(arguments.getString("settingsCa"))))
        val application = application
        for (name in listOf("connection", "encrypted_session", "app-updates")) application.getSharedPreferences(name, 0).edit().clear().commit()
        val store = ViewModelStore()
        lateinit var model: PhoneModel
        val name = "Settings 102 ${if (BuildConfig.IS_TV) "TV" else "Phone"}"
        var createdId: Long? = null
        try {
            compose.runOnUiThread {
                model = ViewModelProvider(store, ViewModelProvider.AndroidViewModelFactory(application))[PhoneModel::class.java]
                compose.activity.setContent {
                    val state by model.state.collectAsState()
                    PhoneTheme {
                        Surface(Modifier.fillMaxSize(), color = phonePalette.background, contentColor = phonePalette.text) {
                            Column(Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing).padding(16.dp)) {
                                if (state.profile != null) ProfileMenu(state, model)
                                if (state.settings == "admin") AdminSettingsScreen(state, model, Modifier.weight(1f))
                            }
                        }
                    }
                }
            }
            settled(model)
            compose.runOnUiThread { model.signInWithPIN(origin, "reviewer", "123456", ca) }
            settled(model)
            assertTrue(model.state.value.error.orEmpty(), model.state.value.signedIn)
            val original = model.state.value.profiles.first { it.role == "admin" && !it.hasPin }
            compose.runOnUiThread { model.selectProfile(original, "") }
            settled(model)
            compose.onNodeWithText(original.name).performClick()
            compose.onNodeWithText("Settings").performClick()
            settled(model)
            for (cycle in listOf(ReleaseCycle.NIGHTLY, ReleaseCycle.RELEASE_CANDIDATE, ReleaseCycle.STABLE)) {
                compose.onNodeWithText(cycle.label).performClick()
                assertEquals(cycle, model.releaseCycle.value)
                assertEquals(cycle, ReleaseCycle.fromStored(application.getSharedPreferences("app-updates", 0).getString("release_cycle", null)))
                val reloaded = ViewModelStore()
                compose.runOnUiThread {
                    try {
                        val restored = ViewModelProvider(reloaded, ViewModelProvider.AndroidViewModelFactory(application))[PhoneModel::class.java]
                        assertEquals(cycle, restored.releaseCycle.value)
                    } finally { reloaded.clear() }
                }
            }
            UiDevice.getInstance(InstrumentationRegistry.getInstrumentation()).takeScreenshot(File(application.getExternalFilesDir(null), "settings-updates.png"))
            if (BuildConfig.IS_TV) {
                compose.onNodeWithText("Stable").assertIsSelected()
                compose.onNodeWithText("Stable").performKeyInput { pressKey(Key.Back) }
                compose.onNodeWithText("Updates").assertIsFocused()
                compose.onNodeWithText("Updates").performKeyInput { pressKey(Key.DirectionDown); pressKey(Key.DirectionRight) }
                compose.onNodeWithText("Add Profile").assertIsFocused()
            } else compose.onNodeWithText("Profiles").performClick()
            compose.onNodeWithText("Add Profile").performClick()
            compose.onNodeWithText("Name").performTextInput("x".repeat(41))
            compose.onNodeWithText("Create").assertIsNotEnabled()
            compose.onNodeWithText("Name").performTextReplacement(name)
            compose.onNodeWithText("Create").performClick()
            settled(model)
            createdId = model.state.value.profiles.single { it.name == name }.id
            fun action(description: String) {
                compose.onNodeWithTag("admin-settings-content").performScrollToNode(hasContentDescription(description))
                compose.onNodeWithContentDescription(description).performClick()
            }
            action("Edit $name")
            compose.onNodeWithText("Name").performTextReplacement("$name Edited")
            compose.onNode(hasText("Viewer", substring = true) and hasAnyAncestor(isDialog()) and hasClickAction()).performClick()
            compose.onNode(hasText("Admin") and hasAnyAncestor(isPopup())).performClick()
            compose.onNode(hasText("Unrestricted", substring = true) and hasAnyAncestor(isDialog()) and hasClickAction()).performClick()
            compose.onNode(hasText("Kids") and hasAnyAncestor(isPopup())).performClick()
            UiDevice.getInstance(InstrumentationRegistry.getInstrumentation()).takeScreenshot(File(application.getExternalFilesDir(null), "settings-profile-editor.png"))
            compose.onNodeWithText("Save Changes").performClick()
            settled(model)
            val editedName = "$name Edited"
            assertEquals("admin", model.state.value.profiles.single { it.id == createdId }.role)
            assertEquals("young_child", model.state.value.profiles.single { it.id == createdId }.tier)
            compose.onNodeWithTag("admin-settings-content").performScrollToNode(hasText("Add Profile"))
            compose.onNodeWithText("Add Profile").performClick()
            compose.onNodeWithText("Name").performTextInput(editedName)
            compose.onNodeWithText("Create").performClick()
            settled(model)
            compose.onNodeWithText("A profile with that name already exists.").assertIsDisplayed()
            compose.onNodeWithText("Name").assertTextContains(editedName)
            compose.onNodeWithText("Cancel").performClick()
            val api = ServerClient(origin, SessionStore(application), ca)
            runBlocking { api.request("/api/profiles/$createdId/pin", "PUT", JSONObject().put("pin", "4827")) }
            compose.runOnUiThread { model.refreshProfiles() }
            settled(model)
            action("Reset PIN for $editedName")
            if (BuildConfig.IS_TV) compose.onNodeWithText("Cancel").assertIsFocused()
            compose.onNodeWithText("Cancel").performClick()
            assertTrue(model.state.value.profiles.single { it.id == createdId }.hasPin)
            action("Reset PIN for $editedName")
            compose.onNodeWithText("Reset PIN").performClick()
            settled(model)
            assertFalse(model.state.value.profiles.single { it.id == createdId }.hasPin)
            action("Delete $editedName")
            compose.onNodeWithText("Cancel").performClick()
            assertTrue(model.state.value.profiles.any { it.id == createdId })
            compose.runOnUiThread { model.updateManagedProfile(Long.MAX_VALUE, JSONObject().put("name", "Foreign")) }
            settled(model)
            assertEquals("This profile is no longer available.", model.state.value.error)
            compose.runOnUiThread { model.dismissError() }
            UiDevice.getInstance(InstrumentationRegistry.getInstrumentation()).takeScreenshot(File(application.getExternalFilesDir(null), "settings-profiles.png"))
            compose.runOnUiThread { model.selectProfile(model.state.value.profiles.single { it.id == createdId }, "") }
            settled(model)
            compose.runOnUiThread { model.openSettings("admin"); model.updateManagedProfile(createdId!!, JSONObject().put("role", "viewer")) }
            settled(model)
            assertEquals("viewer", model.state.value.profile?.role)
            assertNull(model.state.value.settings)
            compose.runOnUiThread {
                model.openSettings("admin")
                model.setReleaseCycle(ReleaseCycle.NIGHTLY)
                model.deleteManagedProfile(original.id)
            }
            settled(model)
            assertNull(model.state.value.settings)
            assertEquals(ReleaseCycle.STABLE, model.releaseCycle.value)
            assertTrue(model.state.value.error.orEmpty().contains("Admin profile"))
            assertTrue(model.state.value.profiles.any { it.id == original.id })
            compose.runOnUiThread { model.selectProfile(original, "") }
            settled(model)
            compose.runOnUiThread { model.updateManagedProfile(createdId!!, JSONObject().put("role", "admin")) }
            settled(model)
            compose.runOnUiThread { model.selectProfile(model.state.value.profiles.single { it.id == createdId }, "") }
            settled(model)
            compose.runOnUiThread { model.openSettings("admin") }
            settled(model)
            compose.onNodeWithText("Profiles").performClick()
            action("Delete $editedName")
            compose.onNodeWithText("Delete", substring = false).performClick()
            settled(model)
            assertFalse(model.state.value.profiles.any { it.id == createdId })
            assertNull(model.state.value.profile)
            assertNull(model.state.value.settings)
            assertFalse(application.getSharedPreferences("connection", 0).contains("profile_id"))
            createdId = null
            compose.runOnUiThread { model.selectProfile(original, "") }
            settled(model)
            if (model.state.value.profiles.count { it.role == "admin" } == 1) {
                compose.runOnUiThread { model.deleteManagedProfile(original.id) }
                settled(model)
                assertEquals("Keep at least one Admin profile in this account.", model.state.value.error)
                assertEquals(original.id, model.state.value.profile?.id)
            }
        } finally {
            createdId?.let { id ->
                runBlocking { runCatching { ServerClient(origin, SessionStore(application), ca).request("/api/profiles/$id", "DELETE") } }
            }
            compose.runOnUiThread { store.clear() }
            for (pref in listOf("connection", "encrypted_session", "app-updates")) application.getSharedPreferences(pref, 0).edit().clear().commit()
        }
    }
}
