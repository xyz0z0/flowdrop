package dev.flowdrop.android

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import dev.flowdrop.android.data.ConnState
import dev.flowdrop.android.data.Item
import dev.flowdrop.android.ui.FlowDropTheme
import dev.flowdrop.android.ui.InboxScreen
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test

/**
 * Inbox UI states rendered from explicit [UiState] values: empty inbox,
 * paging button, connection label, upload progress and item actions.
 *
 * These are state-driven, so they do not need a Hub or the ViewModel.
 */
class InboxScreenTest {

    @get:Rule
    val compose = createComposeRule()

    private fun item(
        id: String,
        type: String,
        text: String? = null,
        blobName: String? = null,
        blobSize: Long = 0,
    ) = Item(
        id = id,
        type = type,
        text = text,
        senderDevice = "Kubuntu",
        createdAt = 1_700_000_000_000,
        blob = blobName?.let {
            dev.flowdrop.android.data.Blob(
                id = "b-$id",
                name = it,
                contentType = if (type == "image") "image/png" else "application/octet-stream",
                sizeBytes = blobSize,
                sha256 = "a".repeat(64),
                createdAt = 1_700_000_000_000,
            )
        },
    )

    private fun show(
        state: UiState,
        onLoadMore: () -> Unit = {},
        onDelete: (Item) -> Unit = {},
        onSave: (Item) -> Unit = {},
        onInstall: (Item) -> Unit = {},
        onLogout: () -> Unit = {},
    ) {
        compose.setContent {
            FlowDropTheme {
                InboxScreen(
                    state = state,
                    composerText = "",
                    imageStore = dev.flowdrop.android.ui.AuthenticatedImageStore { null },
                    onComposerChange = {},
                    onSend = {},
                    onPickImages = {},
                    onPickFiles = {},
                    onCancelUpload = {},
                    onDismissUpload = {},
                    onRefresh = {},
                    onLoadMore = onLoadMore,
                    onDelete = onDelete,
                    onSave = onSave,
                    onInstallApk = onInstall,
                    onCancelDownload = {},
                    onDeviceNameChange = {},
                    onLogout = onLogout,
                    onNotify = { _, _ -> },
                )
            }
        }
    }

    private fun baseState() = UiState(
        phase = Phase.INBOX,
        hubAddress = "http://hub.lan:8080",
        deviceName = "测试手机",
        conn = ConnState.OPEN,
    )

    @Test
    fun emptyInboxShowsHint() {
        show(baseState())
        compose.onNodeWithText("收件箱是空的").assertIsDisplayed()
    }

    @Test
    fun showsLiveConnectionLabel() {
        show(baseState())
        compose.onNodeWithText("实时同步中").assertIsDisplayed()
    }

    @Test
    fun showsOfflineLabelWhenClosed() {
        show(baseState().copy(conn = ConnState.CLOSED))
        compose.onNodeWithText("已离线，重连中").assertIsDisplayed()
    }

    @Test
    fun textItemShowsCopyAndDelete() {
        show(baseState().copy(items = listOf(item("1", "text", text = "hello world"))))
        compose.onNodeWithText("hello world").assertIsDisplayed()
        compose.onNodeWithText("复制").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("删除").performScrollTo().assertIsDisplayed()
    }

    @Test
    fun linkItemShowsOpenLink() {
        show(
            baseState().copy(
                items = listOf(item("2", "link", text = "https://example.com/x")),
            ),
        )
        compose.onNodeWithText("打开链接").performScrollTo().assertIsDisplayed()
    }

    @Test
    fun apkItemShowsInstallAction() {
        show(
            baseState().copy(
                items = listOf(item("3", "file", blobName = "app-debug.apk", blobSize = 9_828_033)),
            ),
        )
        compose.onNodeWithText("Android 安装包").assertIsDisplayed()
        compose.onNodeWithText("安装").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("下载").performScrollTo().assertIsDisplayed()
    }

    @Test
    fun nonApkFileHasNoInstallAction() {
        show(
            baseState().copy(
                items = listOf(item("4", "file", blobName = "report.pdf", blobSize = 1024)),
            ),
        )
        compose.onNodeWithText("report.pdf").assertIsDisplayed()
        assertEquals(
            0,
            compose.onAllNodes(hasText("安装")).fetchSemanticsNodes().size,
        )
    }

    @Test
    fun pagingButtonHiddenWithoutCursor() {
        show(baseState().copy(items = listOf(item("5", "text", text = "a")), nextCursor = ""))
        assertEquals(
            0,
            compose.onAllNodes(hasText("加载更早的内容")).fetchSemanticsNodes().size,
        )
    }

    @Test
    fun pagingButtonShownWithCursor() {
        show(baseState().copy(items = listOf(item("6", "text", text = "b")), nextCursor = "cur"))
        compose.onNodeWithText("加载更早的内容").performScrollTo().assertIsDisplayed()
    }

    @Test
    fun loadMoreIsInvoked() {
        var calls = 0
        show(
            baseState().copy(items = listOf(item("7", "text", text = "c")), nextCursor = "cur"),
            onLoadMore = { calls++ },
        )
        compose.onNodeWithText("加载更早的内容").performScrollTo().performClick()
        assertEquals(1, calls)
    }

    @Test
    fun uploadProgressRowIsShownAndCancellable() {
        show(
            baseState().copy(
                uploads = listOf(
                    UploadTask(
                        key = "u1",
                        name = "video.mp4",
                        size = 10_000,
                        loaded = 5_000,
                        status = TaskStatus.RUNNING,
                    ),
                ),
            ),
        )
        compose.onNodeWithText("video.mp4").assertIsDisplayed()
        compose.onNodeWithText("上传中 50%  4.9 KiB / 9.8 KiB").assertIsDisplayed()
        compose.onNodeWithText("取消").assertIsDisplayed()
    }

    @Test
    fun failedUploadShowsError() {
        show(
            baseState().copy(
                uploads = listOf(
                    UploadTask(
                        key = "u2",
                        name = "big.bin",
                        size = 100,
                        status = TaskStatus.FAILED,
                        error = "文件超过 Hub 允许的大小上限",
                    ),
                ),
            ),
        )
        compose.onNodeWithText("big.bin").assertIsDisplayed()
        compose.onNodeWithText("文件超过 Hub 允许的大小上限").assertIsDisplayed()
    }

    @Test
    fun sendButtonDisabledWhenComposerEmpty() {
        show(baseState())
        compose.onNodeWithText("发送").assertIsNotEnabled()
    }

    @Test
    fun errorBannerShowsRetry() {
        show(baseState().copy(error = "无法连接 Hub，请检查地址、网络与 Hub 是否运行"))
        compose.onNodeWithText("无法连接 Hub，请检查地址、网络与 Hub 是否运行").assertIsDisplayed()
        compose.onNodeWithText("重试").assertIsDisplayed()
    }

    @Test
    fun logoutInvokesCallback() {
        var loggedOut = false
        show(baseState(), onLogout = { loggedOut = true })
        compose.onNodeWithText("退出").performClick()
        assertEquals(true, loggedOut)
    }

    @Test
    fun deleteRequiresConfirmation() {
        var deleted: Item? = null
        show(
            baseState().copy(items = listOf(item("8", "text", text = "to remove"))),
            onDelete = { deleted = it },
        )
        compose.onNodeWithText("删除").performScrollTo().performClick()
        assertEquals("first tap must not delete", null, deleted)
        compose.onNodeWithText("确认删除").performScrollTo().performClick()
        assertEquals("8", deleted?.id)
    }

    @Test
    fun downloadProgressIsRendered() {
        show(
            baseState().copy(
                items = listOf(item("9", "file", blobName = "data.zip", blobSize = 1000)),
                downloads = listOf(
                    DownloadTask(
                        key = "save:9",
                        itemId = "9",
                        name = "data.zip",
                        loaded = 250,
                        total = 1000,
                        status = TaskStatus.RUNNING,
                    ),
                ),
            ),
        )
        compose.onNodeWithText("下载中 25%").assertIsDisplayed()
    }
}
