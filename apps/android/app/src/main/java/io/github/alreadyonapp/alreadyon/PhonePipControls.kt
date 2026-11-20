package io.github.alreadyonapp.alreadyon

import android.app.Activity
import android.app.PendingIntent
import android.app.PictureInPictureParams
import android.app.RemoteAction
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.graphics.drawable.Icon
import android.os.Build
import android.util.Rational
import androidx.core.content.ContextCompat

internal class PhonePipControls(private val activity: Activity, private val model: PhoneModel) {
    private val playbackAction = "${activity.packageName}.pip.PLAYBACK"
    private val audioAction = "${activity.packageName}.pip.AUDIO"
    private var canEnter = false
    private var params: PictureInPictureParams? = null

    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            if (!activity.isInPictureInPictureMode || model.state.value.playback == null) return
            when (intent.action) {
                playbackAction -> model.togglePause()
                audioAction -> model.toggleMute()
            }
        }
    }

    fun register() {
        val filter = IntentFilter(playbackAction).apply { addAction(audioAction) }
        ContextCompat.registerReceiver(activity, receiver, filter, ContextCompat.RECEIVER_NOT_EXPORTED)
    }

    fun unregister() {
        activity.unregisterReceiver(receiver)
    }

    fun update(hasPlayback: Boolean, paused: Boolean, muted: Boolean) {
        canEnter = hasPlayback && !paused
        val actions = if (hasPlayback) listOf(
            action(playbackAction, if (paused) R.drawable.pip_play else R.drawable.pip_pause, if (paused) "Play" else "Pause"),
            action(audioAction, if (muted) R.drawable.pip_muted else R.drawable.pip_volume, if (muted) "Unmute" else "Mute"),
        ) else emptyList()

        // Android lays out these actions in its PiP menu and retains its system controls.
        val builder = PictureInPictureParams.Builder().setAspectRatio(Rational(16, 9)).setActions(actions)
        if (Build.VERSION.SDK_INT >= 31) builder.setAutoEnterEnabled(canEnter)
        val updated = builder.build()
        params = updated
        activity.setPictureInPictureParams(updated)
    }

    fun enterOnOlderAndroid() {
        if (Build.VERSION.SDK_INT < 31 && canEnter) {
            params?.let { activity.enterPictureInPictureMode(it) }
        }
    }

    private fun action(name: String, drawable: Int, label: String): RemoteAction {
        val intent = Intent(name).setPackage(activity.packageName)
        val pending = PendingIntent.getBroadcast(activity, 0, intent, PendingIntent.FLAG_IMMUTABLE)
        return RemoteAction(Icon.createWithResource(activity, drawable), label, label, pending)
    }
}
