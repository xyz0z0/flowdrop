package dev.flowdrop.android

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextClearance
import androidx.compose.ui.test.performTextInput
import dev.flowdrop.android.discovery.DiscoveredHub
import dev.flowdrop.android.discovery.DiscoveryState
import dev.flowdrop.android.discovery.DiscoveryStatus
import dev.flowdrop.android.ui.FlowDropTheme
import dev.flowdrop.android.ui.LoginScreen
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

/**
 * Pins the pairing form: four-digit code with leading zeros, no submission
 * before a complete code, pairing vs. reconnect of an already paired Hub, and
 * the unchanged discovery/address behaviour.
 */
class LoginScreenTest {

    @get:Rule
    val compose = createComposeRule()

    private fun setLogin(
        address: String = "http://192.168.1.50:8080",
        pairingCode: String = "",
        hasStoredPairing: Boolean = false,
        hubName: String = "",
        error: String? = null,
        discovery: DiscoveryState = DiscoveryState(),
        onHubChange: (String) -> Unit = {},
        onPairingCodeChange: (String) -> Unit = {},
        onSubmit: () -> Unit = {},
        onRetryConnection: () -> Unit = {},
        onSelectHub: (DiscoveredHub) -> Unit = {},
        onSearchDiscovery: () -> Unit = {},
        onRefreshDiscovery: () -> Unit = {},
        onStopDiscovery: () -> Unit = {},
    ) {
        compose.setContent {
            FlowDropTheme {
                LoginScreen(
                    hubAddress = address,
                    pairingCode = pairingCode,
                    hasStoredPairing = hasStoredPairing,
                    hubName = hubName,
                    loggingIn = false,
                    error = error,
                    onHubChange = onHubChange,
                    onPairingCodeChange = onPairingCodeChange,
                    onSubmit = onSubmit,
                    onRetryConnection = onRetryConnection,
                    discovery = discovery,
                    onSearchDiscovery = onSearchDiscovery,
                    onRefreshDiscovery = onRefreshDiscovery,
                    onStopDiscovery = onStopDiscovery,
                    onSelectHub = onSelectHub,
                )
            }
        }
    }

    private val nearbyHub = DiscoveredHub(
        serviceName = "FlowDrop-客厅",
        displayName = "客厅",
        host = "192.168.1.77",
        port = 8080,
    )

    @Test
    fun showsAddressAndPairingCodeFields() {
        setLogin()
        compose.onNodeWithText("电脑地址").assertIsDisplayed()
        compose.onNodeWithText("配对码").assertIsDisplayed()
        compose.onNodeWithText("配对").assertIsDisplayed()
    }

    @Test
    fun showsErrorText() {
        setLogin(error = "配对码无效或已过期，请在 Hub 上重新查看")
        compose.onNodeWithText("配对码无效或已过期，请在 Hub 上重新查看").assertIsDisplayed()
    }

    @Test
    fun pairButtonIsDisabledWithoutACompleteCode() {
        var submitted = false
        setLogin(pairingCode = "042", onSubmit = { submitted = true })
        compose.onNodeWithText("配对").performClick()
        assertFalse("a three-digit code must not submit", submitted)
    }

    @Test
    fun pairButtonSubmitsWithFourDigitCode() {
        var submitted = false
        setLogin(pairingCode = "0429", onSubmit = { submitted = true })
        compose.onNodeWithText("配对").performClick()
        assertTrue(submitted)
    }

    @Test
    fun typingKeepsLeadingZeroAndOnlyFourDigits() {
        var lastCode = ""
        compose.setContent {
            var code by remember { mutableStateOf("") }
            FlowDropTheme {
                LoginScreen(
                    hubAddress = "http://192.168.1.50:8080",
                    pairingCode = code,
                    hasStoredPairing = false,
                    hubName = "",
                    loggingIn = false,
                    error = null,
                    onHubChange = {},
                    onPairingCodeChange = {
                        code = it
                        lastCode = it
                    },
                    onSubmit = {},
                    onRetryConnection = {},
                )
            }
        }
        // Letters are dropped, the leading zero is kept, the fifth digit is cut.
        compose.onNodeWithText("配对码").performTextInput("04a299")
        assertEquals("0429", lastCode)
        compose.onNodeWithText("0429").assertIsDisplayed()
    }

    @Test
    fun storedPairingOffersReconnectInsteadOfPairing() {
        var reconnected = false
        var submitted = false
        setLogin(
            pairingCode = "0429",
            hasStoredPairing = true,
            hubName = "客厅里的电脑",
            onSubmit = { submitted = true },
            onRetryConnection = { reconnected = true },
        )

        compose.onNodeWithText("已配对的电脑").assertIsDisplayed()
        compose.onNodeWithText("客厅里的电脑").assertIsDisplayed()
        compose.onNodeWithText("重连").performClick()

        assertTrue("reconnect must reuse the saved pairing", reconnected)
        assertFalse("reconnect must not start a new pairing", submitted)
    }

    @Test
    fun showsNearbyHubNameAndAddress() {
        setLogin(
            discovery = DiscoveryState(
                status = DiscoveryStatus.FINISHED,
                hubs = listOf(nearbyHub),
            ),
        )
        compose.onNodeWithText("客厅").assertIsDisplayed()
        compose.onNodeWithText("http://192.168.1.77:8080").assertIsDisplayed()
        compose.onNodeWithText("发现 1 个 Hub").assertIsDisplayed()
    }

    @Test
    fun selectingNearbyHubOnlyRequestsAddressFill() {
        var submitted = false
        var selected: DiscoveredHub? = null
        setLogin(
            pairingCode = "0429",
            discovery = DiscoveryState(
                status = DiscoveryStatus.FINISHED,
                hubs = listOf(nearbyHub),
            ),
            onSelectHub = { selected = it },
            onSubmit = { submitted = true },
        )

        compose.onNodeWithText("客厅").performClick()

        assertEquals(nearbyHub, selected)
        // Selection must not pair: the user still presses 配对 with a code.
        assertFalse("selection must not submit the pairing form", submitted)
    }

    @Test
    fun idleSearchButtonStartsDiscovery() {
        var searched = false
        setLogin(onSearchDiscovery = { searched = true })
        compose.onNodeWithText("搜索").performClick()
        assertTrue(searched)
    }

    @Test
    fun searchingShowsStatusAndStopButton() {
        var stopped = false
        setLogin(
            discovery = DiscoveryState(status = DiscoveryStatus.SEARCHING),
            onStopDiscovery = { stopped = true },
        )
        compose.onNodeWithText("正在搜索附近的 Hub…").assertIsDisplayed()
        compose.onNodeWithText("停止").performClick()
        assertTrue(stopped)
    }

    @Test
    fun emptyResultExplainsManualFallback() {
        setLogin(discovery = DiscoveryState(status = DiscoveryStatus.FINISHED))
        compose.onNodeWithText("请手动输入地址", substring = true).assertIsDisplayed()
        compose.onNodeWithText("刷新").assertIsDisplayed()
    }

    @Test
    fun discoveryFailureDoesNotBlockManualPairing() {
        var submitted = false
        setLogin(
            pairingCode = "0429",
            discovery = DiscoveryState(
                status = DiscoveryStatus.ERROR,
                message = "此设备不支持网络服务发现",
            ),
            onSubmit = { submitted = true },
        )
        compose.onNodeWithText("此设备不支持网络服务发现", substring = true).assertIsDisplayed()
        compose.onNodeWithText("配对").performClick()
        assertTrue("manual pairing must still work", submitted)
    }

    @Test
    fun manualAddressCanBeEditedWhileDiscoveryIsIdle() {
        var lastAddress = "http://192.168.1.50:8080"
        compose.setContent {
            // A small state holder so the field behaves like the ViewModel's
            // hubAddress (the real screen recomposes on every keystroke).
            var text by remember { mutableStateOf("http://192.168.1.50:8080") }
            FlowDropTheme {
                LoginScreen(
                    hubAddress = text,
                    pairingCode = "",
                    hasStoredPairing = false,
                    hubName = "",
                    loggingIn = false,
                    error = null,
                    onHubChange = {
                        text = it
                        lastAddress = it
                    },
                    onPairingCodeChange = {},
                    onSubmit = {},
                    onRetryConnection = {},
                    discovery = DiscoveryState(status = DiscoveryStatus.IDLE),
                )
            }
        }
        compose.onNodeWithText("电脑地址").performTextClearance()
        compose.onNodeWithText("电脑地址").performTextInput("100.101.102.103:8080")
        assertEquals("100.101.102.103:8080", lastAddress)
        compose.onNodeWithText("100.101.102.103:8080").assertIsDisplayed()
    }

    @Test
    fun warnsAboutCleartextHttp() {
        setLogin()
        compose.onNodeWithText(
            "家庭局域网 HTTP 为明文传输，仅用于可信网络；跨网请使用 Tailscale 地址。配对码只用于换取本机设备凭证，随后立即失效。",
        ).assertIsDisplayed()
    }
}
