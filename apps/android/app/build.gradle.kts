import org.jetbrains.kotlin.gradle.dsl.JvmTarget

// Developer installs have a separate identity from the eventual signed release channel.
plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}
android {
    namespace = "io.github.alreadyonapp.alreadyon"
    compileSdk = 36
    defaultConfig {
        applicationId = "io.github.alreadyonapp.alreadyon"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        minSdk = 26
        targetSdk = 36
        buildConfigField("boolean", "IS_TV", "false")
        versionCode = providers.gradleProperty("androidVersionCode").orNull?.toInt() ?: 23
        versionName = providers.gradleProperty("androidVersionName").orNull ?: "0.1.22-dev"
    }
    buildTypes {
        debug { applicationIdSuffix = providers.gradleProperty("androidDebugSuffix").orNull ?: ".debug" }
        // Release signing is intentionally unconfigured until the owner supplies a key.
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"))
        }
    }
    providers.gradleProperty("androidUpdateTestAssetsDirectory").orNull?.let {
        sourceSets["androidTest"].assets.srcDir(it)
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures { compose = true; buildConfig = true }
}
kotlin { compilerOptions { jvmTarget.set(JvmTarget.JVM_17) } }

dependencies {
    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test:rules:1.7.0")
    androidTestImplementation("androidx.test.uiautomator:uiautomator:2.3.0")
    androidTestImplementation(platform("androidx.compose:compose-bom:2025.12.00"))
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    implementation(platform("androidx.compose:compose-bom:2025.12.00"))
    implementation("androidx.activity:activity-compose:1.12.4")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.9.4")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.9.4")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")
    implementation("com.squareup.okhttp3:okhttp:5.5.0")
    implementation("androidx.media3:media3-exoplayer:1.11.1")
    implementation("androidx.media3:media3-exoplayer-hls:1.11.1")
    implementation("androidx.media3:media3-ui-compose:1.11.1")
    implementation("androidx.media3:media3-datasource-okhttp:1.11.1")
}
