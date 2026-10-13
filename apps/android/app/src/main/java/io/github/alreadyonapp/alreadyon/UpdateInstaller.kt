package io.github.alreadyonapp.alreadyon

import android.app.Activity
import android.app.ActivityOptions
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInfo
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import java.io.File
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive

/** Only updater state is stored here; account/session storage is never touched. */
internal class UpdateInstallStore(private val context: Context) {
    val preferences = context.getSharedPreferences("apk-update", Context.MODE_PRIVATE)
    val directory get() = File(context.filesDir, "apk-update").apply { mkdirs() }
    val apk get() = File(directory, "update.apk")
    fun clear() {
        directory.deleteRecursively()
        preferences.edit().clear().commit()
    }
    fun session(): PackageInstaller.SessionInfo? = preferences.getInt("session", -1).let {
        if (it < 0) null else context.packageManager.packageInstaller.getSessionInfo(it)
    }
    fun fail(message: String) {
        session()?.let { runCatching { context.packageManager.packageInstaller.abandonSession(it.sessionId) } }
        directory.deleteRecursively()
        preferences.edit().clear().putString("error", message).commit()
    }
    fun completeIfInstalled(): Boolean {
        val expected = preferences.getLong("code", 0)
        if (expected <= 0 || installedVersionCode(context) < expected) return false
        clear()
        return true
    }
}

@Suppress("DEPRECATION")
internal fun packageCode(info: PackageInfo): Long = if (Build.VERSION.SDK_INT >= 28) info.longVersionCode else info.versionCode.toLong()
internal fun installedVersionCode(context: Context): Long = packageCode(context.packageManager.getPackageInfo(context.packageName, 0))

/** Check the actual APK, not just metadata: a different signed target must never replace this shell. */
@Suppress("DEPRECATION")
internal fun verifyUpdateApk(context: Context, file: File, update: AppUpdate) {
    val flags = PackageManager.GET_META_DATA or if (Build.VERSION.SDK_INT >= 28) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES
    val pm = context.packageManager
    val archive = pm.getPackageArchiveInfo(file.absolutePath, flags) ?: error("Invalid APK.")
    val installed = pm.getPackageInfo(context.packageName, flags)
    check(archive.packageName == context.packageName && packageCode(archive) == update.code && archive.versionName == update.version && update.code > packageCode(installed)) {
        "This update does not match the installed app."
    }
    check(archive.applicationInfo?.metaData?.getString("io.github.alreadyonapp.alreadyon.UPDATE_TARGET") == update.target) {
        "This APK is for a different device target."
    }
    fun signers(info: PackageInfo): Set<String> {
        val signatures = if (Build.VERSION.SDK_INT >= 28) info.signingInfo?.apkContentsSigners else info.signatures
        return signatures.orEmpty().map { java.security.MessageDigest.getInstance("SHA-256").digest(it.toByteArray()).joinToString("") { b -> "%02x".format(b) } }.toSet()
    }
    val expected = signers(installed)
    check(expected.isNotEmpty() && signers(archive) == expected) { "Update signing certificate does not match this app." }
}

internal suspend fun stageUpdate(context: Context, update: AppUpdate): Int {
    val store = UpdateInstallStore(context)
    val installer = context.packageManager.packageInstaller
    val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
        setAppPackageName(context.packageName)
        setSize(update.size)
        if (Build.VERSION.SDK_INT >= 31) setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_REQUIRED)
    }
    val id = installer.createSession(params)
    try {
        installer.openSession(id).use { session ->
            session.openWrite("base.apk", 0, update.size).use { output ->
                store.apk.inputStream().use { it.copyTo(output) }
                session.fsync(output)
            }
        }
        currentCoroutineContext().ensureActive()
        // Persist before commit: self-replacement terminates this process and its coroutines.
        check(store.preferences.edit().putInt("session", id).putLong("code", update.code)
            .putString("release", update.release).commit()) { "Could not save installation state." }
        return id
    } catch (error: Throwable) {
        installer.abandonSession(id)
        throw error
    }
}

internal fun commitUpdate(context: Context, id: Int) {
    val callback = Intent(context, UpdateInstallActivity::class.java).putExtra("updateSession", id)
    // PackageInstaller must fill EXTRA_STATUS and its confirmation Intent, hence MUTABLE.
    val flags = PendingIntent.FLAG_UPDATE_CURRENT or if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0
    // The user requested this update in a resumed UI. Allow the trusted installer callback
    // to reopen this explicit activity after self-replacement kills our visible process.
    val options = if (Build.VERSION.SDK_INT >= 35) ActivityOptions.makeBasic().apply {
        setPendingIntentCreatorBackgroundActivityStartMode(
            if (Build.VERSION.SDK_INT >= 36) ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOW_ALWAYS
            else ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOWED)
    }.toBundle() else null
    val pending = PendingIntent.getActivity(context, id, callback, flags, options)
    context.packageManager.packageInstaller.openSession(id).use { it.commit(pending.intentSender) }
}

/** Explicit, non-exported callback activity lets Android present its installer while the app is visible. */
class UpdateInstallActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) { super.onCreate(savedInstanceState); handle(intent) }
    override fun onNewIntent(intent: Intent) { super.onNewIntent(intent); setIntent(intent); handle(intent) }
    @Suppress("DEPRECATION")
    private fun handle(result: Intent) {
        val store = UpdateInstallStore(this)
        val id = result.getIntExtra("updateSession", -1)
        if (result.getIntExtra(PackageInstaller.EXTRA_STATUS, -1) == PackageInstaller.STATUS_SUCCESS) {
            store.completeIfInstalled()
            openApp()
            return
        }
        if (id != store.preferences.getInt("session", -2)) { finish(); return }
        when (result.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                val confirmation = if (Build.VERSION.SDK_INT >= 33) result.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
                    else result.getParcelableExtra<Intent>(Intent.EXTRA_INTENT)
                if (confirmation == null) { store.fail("Android could not open the installer."); openApp(); return }
                try { startActivityForResult(confirmation, 1) }
                catch (_: Exception) { store.fail("Android could not open the installer."); openApp() }
            }
            PackageInstaller.STATUS_SUCCESS -> { store.completeIfInstalled(); openApp() }
            else -> {
                store.fail(if (result.getIntExtra(PackageInstaller.EXTRA_STATUS, 0) == PackageInstaller.STATUS_FAILURE_ABORTED)
                    "Update installation was cancelled." else "Android could not install the update. The current app is unchanged.")
                openApp()
            }
        }
    }
    @Deprecated("Installer return is a fallback; the session callback supplies the final result.")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        openApp()
    }
    private fun openApp() { startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_NEW_TASK)); finish() }
}

/** A replacement can race the install callback. Cleanup is idempotent and also runs on next launch. */
class UpdateReplacedReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return
        val store = UpdateInstallStore(context)
        if (store.completeIfInstalled()) {
            // Some OEMs restrict background relaunch; the installer Open action/manual launch remains usable.
            runCatching { context.startActivity(Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)) }
        }
    }
}
