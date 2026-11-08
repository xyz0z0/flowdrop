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

class LoginScreenTest {

    @get:Rule
    val compose = createComposeRule()

    private fun setLogin(
        address: String = "http://192.168.1.50:8080",
        username: String = "admin",
        password: String = "",
        error: String? = null,
        discovery: DiscoveryState = DiscoveryState(),
        onHubChange: (String) -> Unit = {},
        onSubmit: () -> Unit = {},
        onSelectHub: (DiscoveredHub) -> Unit = {},
        onSearchDiscovery: () -> Unit = {},
        onRefreshDiscovery: () -> Unit = {},
        onStopDiscovery: () -> Unit = {},
    ) {
        compose.setContent {
            FlowDropTheme {
                LoginScreen(
                    hubAddress = address,
                    username = username,
                    password = password,
                    loggingIn = false,
                    error = error,
                    onHubChange = onHubChange,
                    onUsernameChange = {},
                    onPasswordChange = {},
                    onSubmit = onSubmit,
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
    fun showsHubUserAndPasswordFields() {
        setLogin()
        compose.onNodeWithText("Hub 地址").assertIsDisplayed()
        compose.onNodeWithText("用户名").assertIsDisplayed()
        compose.onNodeWithText("管理员密码").assertIsDisplayed()
        compose.onNodeWithText("登录").assertIsDisplayed()
    }

    @Test
    fun showsErrorText() {
        setLogin(error = "用户名或密码不正确")
        compose.onNodeWithText("用户名或密码不正确").assertIsDisplayed()
    }

    @Test
    fun loginButtonIsDisabledWithoutPassword() {
        var submitted = false
        setLogin(password = "", onSubmit = { submitted = true })
        compose.onNodeWithText("登录").assertIsDisplayed()
        compose.onNodeWithText("登录").performClick()
        assertEquals(false, submitted)
    }

    @Test
    fun loginButtonSubmitsWithPassword() {
        var submitted = false
        setLogin(password = "secret", onSubmit = { submitted = true })
        compose.onNodeWithText("登录").performClick()
        assertEquals(true, submitted)
    }

    @Test
    fun submitsOnImeDone() {
        var submitted = false
        setLogin(password = "secret", onSubmit = { submitted = true })
        compose.onNodeWithText("登录").performClick()
        assertEquals(true, submitted)
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
            password = "secret",
            discovery = DiscoveryState(
                status = DiscoveryStatus.FINISHED,
                hubs = listOf(nearbyHub),
            ),
            onSelectHub = { selected = it },
            onSubmit = { submitted = true },
        )

        compose.onNodeWithText("客厅").performClick()

        assertEquals(nearbyHub, selected)
        // Selection must not log in: the user still presses 登录 with a password.
        assertFalse("selection must not submit the login form", submitted)
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
    fun discoveryFailureDoesNotBlockManualLogin() {
        var submitted = false
        setLogin(
            password = "secret",
            discovery = DiscoveryState(
                status = DiscoveryStatus.ERROR,
                message = "此设备不支持网络服务发现",
            ),
            onSubmit = { submitted = true },
        )
        compose.onNodeWithText("此设备不支持网络服务发现", substring = true).assertIsDisplayed()
        compose.onNodeWithText("登录").performClick()
        assertTrue("manual login must still work", submitted)
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
                    username = "admin",
                    password = "",
                    loggingIn = false,
                    error = null,
                    onHubChange = {
                        text = it
                        lastAddress = it
                    },
                    onUsernameChange = {},
                    onPasswordChange = {},
                    onSubmit = {},
                    discovery = DiscoveryState(status = DiscoveryStatus.IDLE),
                )
            }
        }
        compose.onNodeWithText("Hub 地址").performTextClearance()
        compose.onNodeWithText("Hub 地址").performTextInput("100.101.102.103:8080")
        assertEquals("100.101.102.103:8080", lastAddress)
        compose.onNodeWithText("100.101.102.103:8080").assertIsDisplayed()
    }

    @Test
    fun warnsAboutCleartextHttp() {
        setLogin()
        compose.onNodeWithText(
            "家庭局域网 HTTP 为明文传输，仅用于可信网络；跨网请使用 Tailscale 地址。密码仅用于换取会话，随后立即丢弃。",
        ).assertIsDisplayed()
    }
}
