# Third-party notices / 第三方软件声明

FlowDrop itself is licensed under the MIT License; see [LICENSE](LICENSE).
FlowDrop 本体采用 MIT 许可，见 [LICENSE](LICENSE)。

This file lists the third-party components that are actually present in the
distributed FlowDrop artifacts, together with their upstream versions and
license families. Full license texts are stored under
[`third_party/licenses/`](third_party/licenses), mirroring the upstream files
(copyright lines, license text, and upstream `NOTICE`/`PATENTS` files where the
upstream project ships them).

本文件列出**实际随发行产物分发**的第三方组件及其上游版本与许可，许可全文与
上游 `NOTICE`/`PATENTS` 位于 [`third_party/licenses/`](third_party/licenses)。

## Distributed artifacts and what they contain

Versions below describe what the release build actually ships (they are the
versions resolved by the current build configuration and embedded in the built
binaries), not merely the versions requested by a build file.

| Artifact | Contents relevant to licensing |
| --- | --- |
| CLI archives (Linux, macOS and Windows; amd64 and arm64) | one statically linked Go binary (`CGO_ENABLED=0`, Go 1.26.0) with the web UI embedded |
| Desktop Linux archive | one Go binary, Go 1.26.0, `CGO_ENABLED=1`, built with tags `gtk3,production`; web UI embedded |
| Desktop Windows archives | `flowdrop-desktop.exe` (Go 1.26.0, `CGO_ENABLED=0`) plus FlowDrop's own `README.txt`; web UI embedded |
| Android APK | `classes.dex`, `classes2.dex`, JNI libraries, Android resources and `assets/` |

How the component lists can be reproduced:

- Go modules and the Go toolchain: `go version -m <binary>` (reads the embedded
  build info; no need to run the binary).
- Android: `./gradlew :app:dependencies --configuration releaseRuntimeClasspath`
  and the resolved artifact list of the same configuration, cross-checked
  against the `META-INF/*.version` entries and the dex contents inside a built
  release APK.
- Web UI: `web/package-lock.json` resolved versions and the emitted production
  bundle (embedded in the Go binaries).

## Go standard library

Every distributed Go binary (all CLI archives and both desktop builds) was
compiled with **Go 1.26.0** (read from the embedded build info). The Go runtime
and standard library are covered by the Go license (BSD-3-Clause):

- `third_party/licenses/go-stdlib/LICENSE`
- `third_party/licenses/go-stdlib/PATENTS`

The Go compiler itself is a build tool; only the Go runtime and standard
library code are linked into each binary.

## Go modules linked into the binaries

### CLI binaries (all platforms)

| Module | Version | License |
| --- | --- | --- |
| github.com/cenkalti/backoff | v2.2.1+incompatible | MIT |
| github.com/coder/websocket | v1.8.15 | ISC |
| github.com/dustin/go-humanize | v1.0.1 | MIT |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause (1) |
| github.com/grandcat/zeroconf | v1.0.0 | MIT |
| github.com/mattn/go-isatty | v0.0.20 | MIT |
| github.com/miekg/dns | v1.1.27 | BSD-3-Clause |
| github.com/ncruces/go-strftime | v0.1.9 | MIT |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |
| golang.org/x/crypto | v0.36.0 | BSD-3-Clause |
| golang.org/x/exp | v0.0.0-20250620022241-b7579e27df2b | BSD-3-Clause |
| golang.org/x/net | v0.21.0 | BSD-3-Clause |
| golang.org/x/sys | v0.34.0 | BSD-3-Clause |
| modernc.org/libc | v1.66.3 | BSD-3-Clause |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause |
| modernc.org/memory | v1.11.0 | BSD-3-Clause |
| modernc.org/sqlite | v1.38.2 | BSD-3-Clause (2) |

(1) `github.com/google/uuid` is present in the linux/darwin CLI builds; the
Windows builds do not link it (platform-specific code paths).
(2) `modernc.org/sqlite` embeds a SQLite amalgamation compiled from C by
`modernc.org/libc`; see `LICENSE` and `LICENSE-3RD-PARTY.md` for the bundled
licenses.

### Desktop builds (Linux and Windows)

In addition to (or instead of) the CLI set above, the desktop builds link:

| Module | Version | Platforms | License |
| --- | --- | --- | --- |
| github.com/godbus/dbus/v5 | v5.2.2 | linux | BSD-2-Clause |
| github.com/go-ole/go-ole | v1.3.0 | windows | MIT |
| github.com/ncruces/go-strftime | v1.0.0 | windows | MIT |
| github.com/skip2/go-qrcode | v0.0.0-20200617195104-da1b6568686e | linux, windows | MIT |
| github.com/wailsapp/wails/v3 | v3.1.0 | linux, windows | MIT |
| golang.org/x/crypto | v0.57.0 | linux, windows | BSD-3-Clause |
| golang.org/x/exp | v0.0.0-20260410095643-746e56fc9e2f | linux, windows | BSD-3-Clause |
| golang.org/x/net | v0.60.0 | linux, windows | BSD-3-Clause |
| golang.org/x/sys | v0.48.0 | linux, windows | BSD-3-Clause |
| modernc.org/libc | v1.67.6 | linux, windows | BSD-3-Clause |
| modernc.org/sqlite | v1.44.3 | linux, windows | BSD-3-Clause (2) |

The desktop builds also link the following modules shared with the CLI builds,
at the same versions as the CLI table above: `coder/websocket`,
`dustin/go-humanize`, `google/uuid`, `grandcat/zeroconf`, `mattn/go-isatty`,
`miekg/dns`, `remyoudompheng/bigfft`, `cenkalti/backoff`, plus
`modernc.org/mathutil` v1.7.1 and `modernc.org/memory` v1.11.0. The remaining
`golang.org/x/*` and `modernc.org/{libc,sqlite}` versions differ between the CLI
and desktop modules and are listed in the table above. Exact per-binary lists
can be reproduced with `go version -m`.

## Web UI embedded in the server, CLI and desktop binaries

| Component | Version | License |
| --- | --- | --- |
| react | 18.3.1 | MIT |
| react-dom | 18.3.1 | MIT |
| scheduler (transitive of react-dom) | 0.23.2 | MIT |

Texts: `third_party/licenses/web/react/LICENSE`,
`third_party/licenses/web/react-dom/LICENSE`,
`third_party/licenses/web/scheduler/LICENSE`.

`js-tokens` / `loose-envify` are only used at build time; the production bundle
inlines the development/production switch and does not ship them as separate
components.

## Android APK

The APK contains the 81 artifacts that Gradle resolves for the release
`releaseRuntimeClasspath` of `android/app` (excluding test-only and
`debugImplementation` dependencies, which are not shipped). Most are
Apache-2.0; the exceptions are:

- `com.squareup.okhttp3:okhttp:4.12.0` ships
  `okhttp3/internal/publicsuffix/publicsuffixes.gz`, a compiled copy of the
  Public Suffix List, with its upstream
  `okhttp3/internal/publicsuffix/NOTICE`. That data is subject to the
  **Mozilla Public License 2.0**. The NOTICE is preserved as
  `third_party/licenses/android/okhttp-publicsuffix-NOTICE.txt` and the license
  text as `third_party/licenses/android/MPL-2.0.txt`.
- `androidx.camera:camera-core:1.3.4` is declared Apache-2.0 **and** BSD; the
  AAR bundles a JNI build of **libyuv** (`libimage_processing_util_jni.so`,
  BSD-3-Clause with an additional patent grant). See
  `third_party/licenses/android/libyuv-LICENSE.txt` and
  `third_party/licenses/android/libyuv-PATENTS.txt`.
- `androidx.graphics:graphics-path:1.0.1` ships
  `libandroidx.graphics.path.so`, Apache-2.0.

Full Apache-2.0 text: `third_party/licenses/android/Apache-2.0.txt`. Apart from
the okhttp Public Suffix List NOTICE, no resolved Android artifact carries an
upstream `NOTICE` file (checked by listing the entries of every resolved
AAR/JAR), so no further `NOTICE` is reproduced. The Apache-2.0 components are
published by The Android Open Source Project (AndroidX), JetBrains (Kotlin and
KotlinX), Square (OkHttp/Okio), Google (AutoValue annotations, ListenableFuture)
and the ZXing authors.

### Resolved release runtime classpath (81 artifacts)

| Component | Version | License |
| --- | --- | --- |
| `androidx.activity:activity-compose` | 1.8.2 | Apache-2.0 |
| `androidx.activity:activity-ktx` | 1.8.2 | Apache-2.0 |
| `androidx.activity:activity` | 1.8.2 | Apache-2.0 |
| `androidx.annotation:annotation-experimental` | 1.4.0 | Apache-2.0 |
| `androidx.annotation:annotation-jvm` | 1.8.0 | Apache-2.0 |
| `androidx.appcompat:appcompat-resources` | 1.1.0 | Apache-2.0 |
| `androidx.appcompat:appcompat` | 1.1.0 | Apache-2.0 |
| `androidx.arch.core:core-common` | 2.2.0 | Apache-2.0 |
| `androidx.arch.core:core-runtime` | 2.2.0 | Apache-2.0 |
| `androidx.autofill:autofill` | 1.0.0 | Apache-2.0 |
| `androidx.camera:camera-camera2` | 1.3.4 | Apache-2.0 |
| `androidx.camera:camera-core` | 1.3.4 | Apache-2.0 + BSD-3-Clause (libyuv) |
| `androidx.camera:camera-lifecycle` | 1.3.4 | Apache-2.0 |
| `androidx.camera:camera-video` | 1.3.4 | Apache-2.0 |
| `androidx.camera:camera-view` | 1.3.4 | Apache-2.0 |
| `androidx.collection:collection-jvm` | 1.4.0 | Apache-2.0 |
| `androidx.collection:collection-ktx` | 1.4.0 | Apache-2.0 |
| `androidx.compose.animation:animation-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.animation:animation-core-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.foundation:foundation-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.foundation:foundation-layout-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.material3:material3-android` | 1.3.0 | Apache-2.0 |
| `androidx.compose.material:material-icons-core-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.material:material-ripple-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.runtime:runtime-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.runtime:runtime-saveable-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.ui:ui-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.ui:ui-geometry-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.ui:ui-graphics-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.ui:ui-text-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.ui:ui-unit-android` | 1.7.0 | Apache-2.0 |
| `androidx.compose.ui:ui-util-android` | 1.7.0 | Apache-2.0 |
| `androidx.concurrent:concurrent-futures` | 1.1.0 | Apache-2.0 |
| `androidx.core:core-ktx` | 1.13.1 | Apache-2.0 |
| `androidx.core:core` | 1.13.1 | Apache-2.0 |
| `androidx.cursoradapter:cursoradapter` | 1.0.0 | Apache-2.0 |
| `androidx.customview:customview-poolingcontainer` | 1.0.0 | Apache-2.0 |
| `androidx.customview:customview` | 1.0.0 | Apache-2.0 |
| `androidx.drawerlayout:drawerlayout` | 1.0.0 | Apache-2.0 |
| `androidx.emoji2:emoji2` | 1.3.0 | Apache-2.0 |
| `androidx.exifinterface:exifinterface` | 1.3.2 | Apache-2.0 |
| `androidx.fragment:fragment` | 1.1.0 | Apache-2.0 |
| `androidx.graphics:graphics-path` | 1.0.1 | Apache-2.0 |
| `androidx.interpolator:interpolator` | 1.0.0 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-common-java8` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-common-jvm` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-livedata-core-ktx` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-livedata-core` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-livedata` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-process` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-runtime-android` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-runtime-compose-android` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-runtime-ktx-android` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-viewmodel-android` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-viewmodel-compose-android` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-viewmodel-ktx` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-viewmodel-savedstate` | 2.8.3 | Apache-2.0 |
| `androidx.lifecycle:lifecycle-viewmodel` | 2.8.3 | Apache-2.0 |
| `androidx.loader:loader` | 1.0.0 | Apache-2.0 |
| `androidx.profileinstaller:profileinstaller` | 1.3.1 | Apache-2.0 |
| `androidx.savedstate:savedstate-ktx` | 1.2.1 | Apache-2.0 |
| `androidx.savedstate:savedstate` | 1.2.1 | Apache-2.0 |
| `androidx.startup:startup-runtime` | 1.1.1 | Apache-2.0 |
| `androidx.tracing:tracing` | 1.0.0 | Apache-2.0 |
| `androidx.vectordrawable:vectordrawable-animated` | 1.1.0 | Apache-2.0 |
| `androidx.vectordrawable:vectordrawable` | 1.1.0 | Apache-2.0 |
| `androidx.versionedparcelable:versionedparcelable` | 1.1.1 | Apache-2.0 |
| `androidx.viewpager:viewpager` | 1.0.0 | Apache-2.0 |
| `com.google.auto.value:auto-value-annotations` | 1.6.3 | Apache-2.0 |
| `com.google.guava:listenablefuture` | 1.0 | Apache-2.0 |
| `com.google.zxing:core` | 3.5.3 | Apache-2.0 |
| `com.squareup.okhttp3:okhttp` | 4.12.0 | Apache-2.0; bundled Public Suffix List data MPL-2.0 |
| `com.squareup.okio:okio-jvm` | 3.6.0 | Apache-2.0 |
| `org.jetbrains.kotlin:kotlin-stdlib-jdk7` | 1.9.10 | Apache-2.0 |
| `org.jetbrains.kotlin:kotlin-stdlib-jdk8` | 1.9.10 | Apache-2.0 |
| `org.jetbrains.kotlin:kotlin-stdlib` | 2.2.10 | Apache-2.0 |
| `org.jetbrains.kotlinx:kotlinx-coroutines-android` | 1.10.2 | Apache-2.0 |
| `org.jetbrains.kotlinx:kotlinx-coroutines-core-jvm` | 1.10.2 | Apache-2.0 |
| `org.jetbrains.kotlinx:kotlinx-serialization-core-jvm` | 1.8.1 | Apache-2.0 |
| `org.jetbrains.kotlinx:kotlinx-serialization-json-jvm` | 1.8.1 | Apache-2.0 |
| `org.jetbrains:annotations` | 23.0.0 | Apache-2.0 |


## License file index

| License | File |
| --- | --- |
| MIT (FlowDrop) | `LICENSE` |
| Apache-2.0 | `third_party/licenses/android/Apache-2.0.txt` |
| MPL-2.0 (okhttp Public Suffix List data) | `third_party/licenses/android/MPL-2.0.txt`, `third_party/licenses/android/okhttp-publicsuffix-NOTICE.txt` |
| libyuv BSD-3-Clause + patents | `third_party/licenses/android/libyuv-LICENSE.txt`, `third_party/licenses/android/libyuv-PATENTS.txt` |
| Go standard library BSD-3-Clause + patents | `third_party/licenses/go-stdlib/LICENSE`, `third_party/licenses/go-stdlib/PATENTS` |
| Per-module Go licenses (LICENSE / LICENSE-GO / LICENSE-3RD-PARTY.md / PATENTS as shipped upstream) | `third_party/licenses/go/<module>/<version>/` |
| React, ReactDOM, scheduler (MIT) | `third_party/licenses/web/<component>/LICENSE` |

## Where the notices must appear in the distributed artifacts

- **APK**: `android/app/build.gradle.kts` copies `LICENSE`,
  `THIRD_PARTY_NOTICES.md` and `third_party/licenses/` into the variant's
  generated assets, so a release APK contains:
  - `assets/licenses/LICENSE`
  - `assets/licenses/THIRD_PARTY_NOTICES.md`
  - `assets/licenses/third_party/licenses/...`
- **CLI and desktop archives**: release packaging must place `LICENSE`,
  `THIRD_PARTY_NOTICES.md` and `third_party/licenses/` next to the binary, so
  every archive carries the same notices as the APK.

## Not distributed (no notice required)

- Build/test tooling: Node.js, npm, Vite, TypeScript and npm dev dependencies;
  Gradle, the Android Gradle Plugin, the Kotlin compiler and Gradle plugin
  dependencies; the Go toolchain; `tools/winres` and its dependencies
  (`github.com/tc-hib/winres`, `github.com/nfnt/resize`, `golang.org/x/image`),
  which only stamp Windows resources at build time.
- Test-only Android artifacts (`junit`, `mockwebserver`,
  `kotlinx-coroutines-test`, Compose UI test artifacts) and
  `debugImplementation` (`ui-test-manifest`) — not in the release APK.
- System/runtime components that are not bundled: GTK 3 and WebKitGTK 4.1
  (Linux desktop uses the system libraries through CGO), the Microsoft WebView2
  Runtime (Windows desktop), and the Android platform itself.
- HTTP/WebSocket client libraries of the host OS/browser used to reach the Hub.

## Note on the `META-INF/{AL2.0,LGPL2.1}` packaging exclusion

`android/app/build.gradle.kts` excludes `/META-INF/{AL2.0,LGPL2.1}` from the
APK. This only drops license *marker* entries that some jars carry; those names
are not evidence of an LGPL dependency. `releaseRuntimeClasspath` resolves to
Apache-2.0 components plus the MPL-2.0 Public Suffix List data bundled in
okhttp, as listed above; no LGPL component is present.

## Regenerating this inventory

```bash
# Web UI versions (run from the repository root)
node -e 'const l=require("./web/package-lock.json");for (const k of ["node_modules/react","node_modules/react-dom","node_modules/scheduler"]) console.log(k, l.packages[k].version, l.packages[k].license)'

# Go modules + toolchain linked into a release binary (extract an archive to a
# scratch directory first; nothing is executed)
go version -m flowdrop

# Android: resolved release runtime classpath (JDK 17+, Gradle wrapper)
cd android
./gradlew :app:dependencies --configuration releaseRuntimeClasspath

# APK asset check after a release build
unzip -l flowdrop-android-<version>.apk | grep assets/licenses
```
