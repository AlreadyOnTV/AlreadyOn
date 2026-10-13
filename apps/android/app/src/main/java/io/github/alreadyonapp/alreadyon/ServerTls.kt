package io.github.alreadyonapp.alreadyon

import okhttp3.HttpUrl
import okhttp3.OkHttpClient
import java.io.IOException
import java.security.KeyStore
import java.io.ByteArrayInputStream
import java.security.cert.CertificateFactory
import javax.net.ssl.TrustManagerFactory
import java.security.SecureRandom
import java.security.cert.X509Certificate
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

/** Explicit per-server exception; never modify global TLS defaults or allow another origin. */
internal fun OkHttpClient.Builder.serverTls(origin: HttpUrl, bypass: Boolean, trustedCa: String? = null): OkHttpClient.Builder {
    if (!trustedCa.isNullOrBlank()) {
        val certificates = CertificateFactory.getInstance("X.509").generateCertificates(ByteArrayInputStream(trustedCa.toByteArray()))
        require(certificates.isNotEmpty())
        val store = KeyStore.getInstance(KeyStore.getDefaultType()).apply { load(null) }
        certificates.forEachIndexed { index, certificate -> store.setCertificateEntry("server-$index", certificate) }
        val manager = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(store) }
        val trust = manager.trustManagers.filterIsInstance<X509TrustManager>().single()
        val context = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), SecureRandom()) }
        // The accepted instance trust anchor identifies this LAN server, including its IP.
        return sslSocketFactory(context.socketFactory, trust).hostnameVerifier { host, _ -> host == origin.host }
            .followRedirects(false).followSslRedirects(false)
            .addInterceptor { chain ->
                if (!sameOrigin(origin, chain.request().url)) throw IOException("Unexpected server origin.")
                chain.proceed(chain.request())
            }
    }
    if (!bypass) return this
    val trust = object : X509TrustManager {
        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = Unit
        override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) = Unit
    }
    val context = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), SecureRandom()) }
    return sslSocketFactory(context.socketFactory, trust)
        .hostnameVerifier { host, _ -> host == origin.host }
        .followRedirects(false).followSslRedirects(false)
        .addInterceptor { chain ->
            if (!sameOrigin(origin, chain.request().url)) throw IOException("Unexpected server origin.")
            chain.proceed(chain.request())
        }
}
