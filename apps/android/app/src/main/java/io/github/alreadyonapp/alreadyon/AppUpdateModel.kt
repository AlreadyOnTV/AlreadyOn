package io.github.alreadyonapp.alreadyon

import android.app.Application
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update

internal enum class UpdatePhase { AVAILABLE, DOWNLOADING, PERMISSION, INSTALLING, ERROR }
internal data class AppUpdateState(val update: AppUpdate? = null, val phase: UpdatePhase = UpdatePhase.AVAILABLE,
    val progress: Float = 0f, val error: String? = null, val readyToInstall: Boolean = false)

internal class AppUpdateModel(application: Application) : AndroidViewModel(application) {
    private val context = application
    private val store = UpdateInstallStore(application)
    private val service = GitHubUpdates()
    private val mutable = MutableStateFlow(AppUpdateState())
    val state: StateFlow<AppUpdateState> = mutable
    private var work: Job? = null
    private var dismissed = false
    init {
        work = viewModelScope.launch {
            refreshInstallation()
            if (mutable.value.phase == UpdatePhase.INSTALLING || mutable.value.phase == UpdatePhase.ERROR) return@launch
            // Debug/test packages never offer a production APK with a different identity or signing key.
            if (context.packageName != UPDATE_PACKAGE) return@launch
            try {
                val info = context.packageManager.getPackageInfo(context.packageName, 0)
                val update = service.check(info.versionName.orEmpty(), packageCode(info), BuildConfig.IS_TV)
                if (!dismissed) mutable.value = AppUpdateState(update)
            } catch (_: CancellationException) { throw CancellationException() }
            catch (_: Exception) { /* Offline/rate limits must not interrupt startup or playback. */ }
        }
    }
    fun refreshInstallation() {
        if (store.completeIfInstalled()) { mutable.value = AppUpdateState(); return }
        val error = store.preferences.getString("error", null)
        if (error != null) {
            mutable.update { it.copy(phase = UpdatePhase.ERROR, error = error) }
            store.preferences.edit().remove("error").apply()
        } else if (store.session() != null) {
            mutable.update { it.copy(phase = UpdatePhase.INSTALLING, readyToInstall = !store.preferences.getBoolean("committed", false)) }
        } else if (store.preferences.contains("session")) {
            store.clear()
            mutable.update { it.copy(phase = UpdatePhase.ERROR, error = "The installation did not finish. Please retry the update.") }
        } else if (mutable.value.phase != UpdatePhase.DOWNLOADING && mutable.value.phase != UpdatePhase.PERMISSION && mutable.value.phase != UpdatePhase.INSTALLING) {
            store.directory.deleteRecursively() // Abandoned download from a terminated process.
        }
        if (mutable.value.phase == UpdatePhase.PERMISSION && context.packageManager.canRequestPackageInstalls()) install()
    }
    fun update() {
        val update = mutable.value.update ?: return
        if (mutable.value.phase == UpdatePhase.DOWNLOADING || mutable.value.phase == UpdatePhase.INSTALLING) return
        mutable.update { it.copy(phase = UpdatePhase.DOWNLOADING, error = null, progress = 0f) }
        work = viewModelScope.launch {
            try {
                service.download(update, store.apk) { progress -> mutable.update { it.copy(progress = progress) } }
                withContext(Dispatchers.IO) { verifyUpdateApk(context, store.apk, update) }
                if (context.packageManager.canRequestPackageInstalls()) install()
                else mutable.update { it.copy(phase = UpdatePhase.PERMISSION) }
            } catch (error: CancellationException) { throw error }
            catch (error: Exception) { kotlinx.coroutines.currentCoroutineContext().ensureActive(); store.directory.deleteRecursively(); mutable.update { it.copy(phase = UpdatePhase.ERROR, error = error.message ?: "Could not update the app.") } }
        }
    }
    fun permissionIntent() = Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}"))
    fun permissionFailure() { mutable.update { it.copy(phase = UpdatePhase.ERROR, error = "Open Android settings and allow AlreadyOn to install apps, then retry.") } }
    private fun install() {
        val update = mutable.value.update ?: return
        mutable.update { it.copy(phase = UpdatePhase.INSTALLING) }
        work = viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    verifyUpdateApk(context, store.apk, update)
                    stageUpdate(context, update)
                }
                mutable.update { it.copy(readyToInstall = true) }
            } catch (error: CancellationException) {
                store.session()?.let { runCatching { context.packageManager.packageInstaller.abandonSession(it.sessionId) } }
                store.clear()
                throw error
            } catch (error: Exception) {
                store.fail("Could not start installation. Please retry.")
                mutable.update { it.copy(phase = UpdatePhase.ERROR, error = "Could not start installation. Please retry.") }
            }
        }
    }
    // Commit only from a resumed UI. Background completion must not open an installer over another app.
    fun continueInstallation() {
        if (store.preferences.getBoolean("committed", false)) return
        val id = store.preferences.getInt("session", -1)
        if (id < 0) return
        mutable.update { it.copy(readyToInstall = false) }
        try {
            store.preferences.edit().putBoolean("committed", true).commit()
            commitUpdate(context, id)
        } catch (_: Exception) {
            store.fail("Could not open the Android installer. Please retry.")
            mutable.update { it.copy(phase = UpdatePhase.ERROR, error = "Could not open the Android installer. Please retry.") }
        }
    }
    fun cancel() {
        dismissed = true
        work?.cancel(); service.cancel()
        store.session()?.let { runCatching { context.packageManager.packageInstaller.abandonSession(it.sessionId) } }
        store.clear()
        mutable.value = AppUpdateState()
    }
    override fun onCleared() { service.cancel(); super.onCleared() }
}
