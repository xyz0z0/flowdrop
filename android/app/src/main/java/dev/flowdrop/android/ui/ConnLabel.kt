package dev.flowdrop.android

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import dev.flowdrop.android.data.ConnState

/** Connection label shared by the inbox top bar. */
object ConnLabel {
    @Composable
    fun text(state: ConnState): String = when (state) {
        ConnState.OPEN -> stringResource(R.string.conn_open)
        ConnState.CONNECTING -> stringResource(R.string.conn_connecting)
        ConnState.CLOSED -> stringResource(R.string.conn_closed)
        ConnState.PAUSED -> stringResource(R.string.conn_paused)
    }
}
