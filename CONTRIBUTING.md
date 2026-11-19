# Contributing to FlowDrop

Focused bug reports and pull requests help maintainers review changes to
FlowDrop.

## Before you start

- For a security issue, follow [SECURITY.md](SECURITY.md) instead of opening a
  public issue.
- For a change to the protocol, schema or runtime dependencies, consider opening
  an issue to discuss the approach.
- Keep pull requests small and scoped to one problem. Unrelated refactors or
  formatting churn make review harder and may be asked to split.

## Repository layout

| Path | What it is |
| --- | --- |
| `server/` | Go Hub: HTTP API, WebSocket, SQLite, embedded web UI |
| `desktop/` | Go desktop app (Wails v3, GTK3/WebKitGTK on Linux, WebView2 on Windows) |
| `web/` | React + TypeScript frontend, built into the Go binaries |
| `android/` | Native Android app (Kotlin, Jetpack Compose) |
| `scripts/` | Build and packaging helpers |
| `docs/` | Design, deployment, CLI and pairing documentation |
| `tests/` | End-to-end tests |

The user-facing documentation is written in Chinese. Code, comments and commit
messages are usually English. Issues and pull requests can use either language.

## Building and testing

The Go version is listed in each module's `go.mod`. The web UI build uses Node.js
20 or later. Android builds use JDK 17 or later and the Android SDK. For desktop
dependencies, see the [desktop build instructions](README.md#桌面版linuxwails).

```bash
# Hub + web UI (embeds the built frontend)
scripts/build.sh

# Go tests
(cd server && go test ./...)
(cd desktop && go test ./...)

# Web UI
(cd web && npm ci && npm run build)

# Android
(cd android && ./gradlew :app:testDebugUnitTest :app:assembleDebug)
```

Run the checks that cover what you changed. List any checks that need a device
or environment you did not have in the pull request.

## Pull requests

- One logical change per pull request, with a clear description of the problem
  and the approach.
- Include tests for behaviour changes where the project already has tests for
  that area.
- Do not commit secrets, device tokens, keystores, generated build output or
  local configuration.
- Do not reformat or reorder unrelated code.
- If you add, remove or upgrade a runtime dependency, update
  [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and
  `third_party/licenses/` in the same pull request.

## Licensing of contributions

FlowDrop is released under the MIT License (see [LICENSE](LICENSE)). By
submitting a pull request you agree that your contribution is licensed under the
same terms.

## Code of conduct

Participation in this project is covered by
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
