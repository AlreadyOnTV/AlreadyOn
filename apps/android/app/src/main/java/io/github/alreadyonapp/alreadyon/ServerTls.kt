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

/** Automatic LAN instance trust is app-local; system HTTPS trust remains unchanged. */
internal fun OkHttpClient.Builder.serverTls(origin: HttpUrl, trustedCa: String? = null): OkHttpClient.Builder {
    if (!trustedCa.isNullOrBlank()) {
        val certificates = CertificateFactory.getInstance("X.509").generateCertificates(ByteArrayInputStream(trustedCa.toByteArray()))
        require(certificates.isNotEmpty())
        val store = KeyStore.getInstance(KeyStore.getDefaultType()).apply { load(null) }
        val anchors = trustAnchors(certificates.map { it as X509Certificate })
        anchors.forEachIndexed { index, certificate ->
            certificate.checkValidity()
            store.setCertificateEntry("server-$index", certificate)
        }
        val manager = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(store) }
        val trust = manager.trustManagers.filterIsInstance<X509TrustManager>().single()
        val context = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), SecureRandom()) }
        // LAN addresses may differ from the default certificate names. The accepted
        // instance CA authenticates the chain; constrain this exception to its origin.
        return sslSocketFactory(context.socketFactory, trust).hostnameVerifier { host, _ -> host == origin.host }
            .followRedirects(false).followSslRedirects(false)
            .addInterceptor { chain ->
                if (!sameOrigin(origin, chain.request().url)) throw IOException("Unexpected server origin.")
                chain.proceed(chain.request())
            }
    }
    return this
}

/** Public LAN discovery only: refuse all credential-bearing or non-bootstrap requests. */
internal fun OkHttpClient.Builder.bootstrapTls(origin: HttpUrl): OkHttpClient.Builder {
    val trust = object : X509TrustManager {
        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = throw java.security.cert.CertificateException("Client certificates unsupported")
        override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) { require(chain.isNotEmpty()); chain.forEach { it.checkValidity() } }
    }
    val context = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), SecureRandom()) }
    return sslSocketFactory(context.socketFactory, trust).hostnameVerifier { host, _ -> host == origin.host }
        .followRedirects(false).followSslRedirects(false)
        .addInterceptor { chain ->
            val request = chain.request()
            if (!sameOrigin(origin, request.url) || request.method != "GET" ||
                request.url.encodedPath !in setOf("/api/tv/server", "/api/tv/trust") || request.url.query != null ||
                request.header("Authorization") != null || request.header("Cookie") != null) {
                throw IOException("Only public server metadata is allowed before trust is established.")
            }
            chain.proceed(request)
        }
}

/** Compare certificate identities, not PEM formatting, bundle order or renewed intermediates.
 * A self-signed root authenticates its intermediates; bundles without one retain exact pins.
 */
internal fun sameServerTrust(saved: String?, offered: String): Boolean {
    if (saved == null) return false
    fun anchors(pem: String): Set<String> {
        val certificates = CertificateFactory.getInstance("X.509")
            .generateCertificates(ByteArrayInputStream(pem.toByteArray())).map { it as X509Certificate }
        require(certificates.isNotEmpty())
        return trustAnchors(certificates).map { certificateIdentity(it) }.toSet()
    }
    return runCatching { anchors(saved) == anchors(offered) }.getOrDefault(false)
}

private fun certificateIdentity(cert: X509Certificate): String =
    java.security.MessageDigest.getInstance("SHA-256").digest(cert.encoded).joinToString("") { "%02x".format(it) }

internal fun isServerTrustFailure(error: Throwable): Boolean =
    generateSequence(error) { it.cause }.any {
        it is java.security.cert.CertificateException || it is java.security.cert.CertPathValidatorException ||
            it is javax.net.ssl.SSLPeerUnverifiedException
    }

private fun trustAnchors(certificates: List<X509Certificate>): List<X509Certificate> {
    val roots = certificates.filter { cert ->
        cert.subjectX500Principal == cert.issuerX500Principal && runCatching { cert.verify(cert.publicKey) }.isSuccess
    }
    return roots.ifEmpty { certificates }
}
