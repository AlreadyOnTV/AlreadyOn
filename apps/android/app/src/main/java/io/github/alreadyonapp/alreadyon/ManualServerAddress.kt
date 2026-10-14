// Manual entry keeps port suggestions visible and separate from HTTPS URL defaults.
package io.github.alreadyonapp.alreadyon

import java.net.Inet6Address
import java.net.InetAddress

internal fun isServerIp(host: String): Boolean {
    val value = host.trim().removeSurrounding("[", "]")
    if (':' in value) {
        if (!Regex("[0-9a-fA-F:.]+").matches(value)) return false
        return runCatching { InetAddress.getByName(value) is Inet6Address }.getOrDefault(false)
    }
    val parts = value.split('.')
    return parts.size == 4 && parts.all { it.isNotEmpty() && it.all { c -> c in '0'..'9' } && it.toIntOrNull() in 0..255 }
}

internal data class ManualServerAddress(val host: String = "", val port: String = "", val portEdited: Boolean = false) {
    fun withHost(value: String): ManualServerAddress = copy(host = value,
        port = if (portEdited) port else if (isServerIp(value)) "19900" else "")
    fun withPort(value: String): ManualServerAddress = copy(port = value, portEdited = true)

    fun origin(): String {
        val address = host.trim().removeSurrounding("[", "]")
        require(address.isNotEmpty() && address.none { it.isWhitespace() || it in "/?#@%" } && "://" !in address) {
            "Enter a hostname or IP address without a scheme, path or port."
        }
        val ip = isServerIp(address)
        require((':' !in address || ip) && (!Regex("[0-9.]+").matches(address) || ip)) { "Enter a valid hostname or IP address." }
        val enteredPort = port.trim()
        require(enteredPort.isEmpty() || enteredPort.all { it in '0'..'9' } && enteredPort.toIntOrNull() in 1..65535) {
            "Port must be between 1 and 65535, or left blank for HTTPS."
        }
        val authority = if (':' in address) "[$address]" else address
        // Blank means ordinary HTTPS (443), even for an IP; never restore 19900 here.
        return requireNotNull(serverOrigin("https://$authority" + if (enteredPort.isEmpty()) "" else ":$enteredPort")) {
            "Enter a valid hostname or IP address."
        }
    }
}
