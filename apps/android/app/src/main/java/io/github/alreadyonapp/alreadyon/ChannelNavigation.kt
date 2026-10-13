// Shared circular channel selection preserves visible ordering and avoids empty rows.
package io.github.alreadyonapp.alreadyon

/** Surf the visible list, skipping empty channels and avoiding a same-channel retune. */
internal fun nextPlayableChannel(channels: List<GuideChannel>, currentId: Long, direction: Int): GuideChannel? {
    if (direction == 0) return null
    val available = channels.filter { it.programs.isNotEmpty() }
    if (available.isEmpty()) return null
    val current = available.indexOfFirst { it.id == currentId }
    val index = if (current < 0) { if (direction > 0) 0 else available.lastIndex }
        else Math.floorMod(current + if (direction > 0) 1 else -1, available.size)
    return available[index].takeUnless { it.id == currentId }
}
