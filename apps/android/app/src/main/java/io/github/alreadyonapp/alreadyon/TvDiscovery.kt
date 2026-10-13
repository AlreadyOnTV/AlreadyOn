package io.github.alreadyonapp.alreadyon

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import kotlinx.coroutines.*
import kotlinx.coroutines.channels.Channel
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.json.JSONObject
import java.io.IOException
import java.net.InetAddress
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume

internal data class TvServer(val name: String, val origin: String)

/** Bounded, credential-free HTTPS probes; closing the scan cancels every socket. */
internal class TvDiscovery(context: Context, private val found: (TvServer) -> Unit,
    private val failed: (String) -> Unit) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    init {
        scope.launch {
            val manager = context.getSystemService(ConnectivityManager::class.java)
            // Wi-Fi without internet may not be Android's default network. Never scan cellular or VPN routes.
            val network = (listOfNotNull(manager.activeNetwork) + manager.allNetworks).distinct().firstOrNull {
                val caps = manager.getNetworkCapabilities(it)
                caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) == true &&
                    (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) || caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) &&
                    manager.getLinkProperties(it)?.linkAddresses?.any { link -> link.address.address.size == 4 && link.address.isSiteLocalAddress } == true
            }
            val capabilities = network?.let { manager.getNetworkCapabilities(it) }
            val links = network?.let { manager.getLinkProperties(it) }
            val local = links?.linkAddresses?.firstOrNull { it.address.address.size == 4 && it.address.isSiteLocalAddress }
            if (network == null || local == null || capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) != true) {
                failed("No local IPv4 network available. Enter the server address after the search ends.")
                return@launch
            }
            // Scan the device's /24 first on larger LANs. Never enumerate more than /16.
            val candidates = subnetCandidates(local.address.address, local.prefixLength).toList()
            val baseClient = OkHttpClient.Builder().socketFactory(network.socketFactory)
                .connectTimeout(750, TimeUnit.MILLISECONDS).readTimeout(750, TimeUnit.MILLISECONDS)
                .callTimeout(1500, TimeUnit.MILLISECONDS).build()
            withTimeoutOrNull(30_000) {
                while (isActive) {
                    val queue = Channel<String>(64)
                    launch { candidates.forEach { queue.send(it) }; queue.close() }
                    coroutineScope {
                        repeat(32) {
                            launch(Dispatchers.IO) {
                                for (address in queue) {
                                    val origin = "https://$address:19900"
                                    val base = origin.toHttpUrl()
                                    val client = baseClient.newBuilder().bootstrapTls(base).build()
                                    val server = try { probeDiscovery(client, origin) }
                                        finally { client.connectionPool.evictAll() }
                                    if (server != null && isActive) withContext(Dispatchers.Main.immediate) { found(server) }
                                }
                            }
                        }
                    }
                    delay(3000)
                }
            }
        }
    }
    fun close() { scope.cancel() }
}

/** An open port alone is insufficient: require AlreadyOn's protocol and metadata. */
internal suspend fun probeDiscovery(client: OkHttpClient, origin: String): TvServer? = suspendCancellableCoroutine { continuation ->
    val call = client.newCall(Request.Builder().url("$origin/api/tv/server").build())
    continuation.invokeOnCancellation { call.cancel() }
    call.enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) { if (continuation.isActive) continuation.resume(null) }
        override fun onResponse(call: Call, response: Response) {
            val server = runCatching {
                response.use {
                    if (!it.isSuccessful) return@use null
                    val source = it.body?.source() ?: return@use null
                    if (source.request(16 * 1024L + 1)) return@use null
                    val metadata = JSONObject(source.readUtf8())
                    val name = metadata.optString("name")
                    if (metadata.optInt("protocol") != 1 || !metadata.has("pairingEnabled") || (name.isBlank() || (metadata.optString("service") != "alreadyon" && !name.startsWith("AlreadyOn ")))) null
                    else TvServer(name.take(128), origin)
                }
            }.getOrNull()
            if (continuation.isActive) continuation.resume(server)
        }
    })
}

/** Includes only the actual on-link subnet; prioritize nearby addresses on broad LANs. */
internal fun subnetCandidates(address: ByteArray, prefixLength: Int): Sequence<String> = sequence {
    if (address.size != 4 || prefixLength !in 0..32) return@sequence
    val own = address.fold(0L) { value, byte -> (value shl 8) or (byte.toLong() and 255) }
    val prefix = prefixLength.coerceAtLeast(16)
    val mask = (0xffffffffL shl (32 - prefix)) and 0xffffffffL
    val start = own and mask
    val end = start or (mask xor 0xffffffffL)
    val nearbyStart = maxOf(start, own and 0xffffff00L)
    val nearbyEnd = minOf(end, (own and 0xffffff00L) or 255)
    fun eligible(value: Long) = value != own && (prefix >= 31 || (value != start && value != end))
    fun text(value: Long) = listOf(24, 16, 8, 0).joinToString(".") { ((value shr it) and 255).toString() }
    for (value in nearbyStart..nearbyEnd) if (eligible(value)) yield(text(value))
    for (value in start..end) if (value !in nearbyStart..nearbyEnd && eligible(value)) yield(text(value))
}

internal fun privateAddress(address: InetAddress): Boolean = address.isSiteLocalAddress || address.isLoopbackAddress ||
    address.isLinkLocalAddress || (address.address.size == 16 && (address.address[0].toInt() and 0xfe) == 0xfc)
