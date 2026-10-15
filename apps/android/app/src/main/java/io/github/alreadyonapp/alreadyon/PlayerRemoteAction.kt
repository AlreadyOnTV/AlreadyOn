package io.github.alreadyonapp.alreadyon

// A bounded FIFO keeps remote selection ordered with cursor moves across HUD reveal.
internal enum class PlayerRemoteAction { Left, Right, Select, ChannelUp, ChannelDown, Back }
