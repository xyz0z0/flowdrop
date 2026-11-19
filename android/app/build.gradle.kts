import java.io.File
import org.gradle.api.DefaultTask
import org.gradle.api.file.DirectoryProperty
import org.gradle.api.file.RegularFileProperty
import org.gradle.api.tasks.InputDirectory
import org.gradle.api.tasks.InputFile
import org.gradle.api.tasks.OutputDirectory
import org.gradle.api.tasks.TaskAction

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.kotlin.plugin.serialization")
}

android {
    namespace = "dev.flowdrop.android"
    compileSdk = 35

    val keystoreFilePath = System.getenv("ANDROID_KEYSTORE_FILE")?.takeIf { it.isNotBlank() }

    defaultConfig {
        applicationId = "dev.flowdrop.android"
        minSdk = 26
        targetSdk = 35
        versionCode = System.getenv("FLOWDROP_VERSION_CODE")?.toIntOrNull() ?: 1
        versionName = System.getenv("FLOWDROP_VERSION_NAME")?.takeIf { it.isNotBlank() } ?: "0.1.0"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        // Optional live-Hub contract test: -Pandroid.testInstrumentationRunnerArguments.hubUrl=http://10.0.2.2:8080
    }

    if (keystoreFilePath != null) {
        signingConfigs {
            create("release") {
                storeFile = file(keystoreFilePath)
                storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ANDROID_KEY_ALIAS")
                keyPassword = System.getenv("ANDROID_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            if (keystoreFilePath != null) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    packaging {
        resources.excludes += setOf("/META-INF/{AL2.0,LGPL2.1}", "/META-INF/versions/9/previous-compilation-data.bin")
    }

    testOptions {
        unitTests.isReturnDefaultValues = true
    }
}

// Ship the repository's license and third-party notices inside the APK under
// assets/licenses/ (single source of truth: the files at the repository root).
// The APK is readable with e.g.
//   unzip -l flowdrop-android-<version>.apk | grep assets/licenses
abstract class SyncOssLicensesTask : DefaultTask() {
    @get:InputFile
    abstract val projectLicense: RegularFileProperty

    @get:InputFile
    abstract val thirdPartyNotices: RegularFileProperty

    @get:InputDirectory
    abstract val licensesDir: DirectoryProperty

    @get:OutputDirectory
    abstract val outputDir: DirectoryProperty

    @TaskAction
    fun sync() {
        val assetsRoot = outputDir.get().asFile
        val target = File(assetsRoot, "licenses")
        target.deleteRecursively()
        projectLicense.get().asFile.copyTo(File(target, "LICENSE"), overwrite = true)
        thirdPartyNotices.get().asFile.copyTo(File(target, "THIRD_PARTY_NOTICES.md"), overwrite = true)
        licensesDir.get().asFile.copyRecursively(File(target, "third_party/licenses"), overwrite = true)
    }
}

val syncOssLicenses = tasks.register<SyncOssLicensesTask>("syncOssLicenses") {
    projectLicense.set(rootProject.layout.projectDirectory.file("../LICENSE"))
    thirdPartyNotices.set(rootProject.layout.projectDirectory.file("../THIRD_PARTY_NOTICES.md"))
    licensesDir.set(rootProject.layout.projectDirectory.dir("../third_party/licenses"))
    outputDir.set(layout.buildDirectory.dir("generated/ossLicenses"))
}

androidComponents {
    onVariants { variant ->
        variant.sources.assets?.addGeneratedSourceDirectory(syncOssLicenses, SyncOssLicensesTask::outputDir)
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:2024.09.00"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-core")

    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.activity:activity-compose:1.8.2")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.3")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.3")

    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.10.2")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.8.1")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // Pairing QR scanning: CameraX preview/analysis plus the offline zxing-core
    // decoder. No Google Play Services and no ML Kit, so the feature works on
    // devices without GMS.
    val cameraxVersion = "1.3.4"
    implementation("androidx.camera:camera-core:$cameraxVersion")
    implementation("androidx.camera:camera-camera2:$cameraxVersion")
    implementation("androidx.camera:camera-lifecycle:$cameraxVersion")
    implementation("androidx.camera:camera-view:$cameraxVersion")
    implementation("com.google.zxing:core:3.5.3")

    testImplementation("junit:junit:4.13.2")
    testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.10.2")

    androidTestImplementation(platform("androidx.compose:compose-bom:2024.09.00"))
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("androidx.test.ext:junit:1.1.5")
    androidTestImplementation("androidx.test:runner:1.5.2")
    androidTestImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.10.2")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
