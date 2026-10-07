package dev.droidline.agent.ui

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.droidline.agent.Agent
import dev.droidline.agent.R

class MainActivity : ComponentActivity() {
    /** Bumped on every resume so screens re-read permission states the user may have changed in Settings. */
    private var resumeTick by mutableIntStateOf(0)
    private var pairLink by mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Agent.init(this)
        enableEdgeToEdge()
        if (savedInstanceState == null) takePairLink(intent)
        setContent {
            DroidlineTheme {
                Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                    Root(resumeTick, pairLink, onPairLinkHandled = { pairLink = null })
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        takePairLink(intent)
    }

    private fun takePairLink(intent: Intent?) {
        val data = intent?.data ?: return
        if (data.scheme == "droidline" && data.host == "pair") pairLink = data.toString()
    }

    override fun onResume() {
        super.onResume()
        Agent.refreshReady()
        resumeTick++
    }
}

@Composable
private fun Root(tick: Int, pairLink: String?, onPairLinkHandled: () -> Unit) {
    val initial = if (Agent.isPaired) 2 else 0
    var tab by rememberSaveable { mutableIntStateOf(initial) }
    LaunchedEffect(pairLink) { if (pairLink != null) tab = 1 }
    val titles = listOf(R.string.tab_setup, R.string.tab_pair, R.string.tab_status)
    Column(Modifier.fillMaxSize().safeDrawingPadding()) {
        Row(Modifier.fillMaxWidth()) {
            titles.forEachIndexed { i, res ->
                val selected = i == tab
                Column(
                    Modifier
                        .weight(1f)
                        .clickable(role = Role.Tab) { tab = i }
                        .padding(top = 14.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Text(
                        stringResource(res),
                        style = MaterialTheme.typography.titleSmall,
                        fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
                        color = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Box(
                        Modifier
                            .padding(top = 10.dp)
                            .fillMaxWidth()
                            .height(2.dp)
                            .background(if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.background)
                    )
                }
            }
        }
        HorizontalDivider()
        when (tab) {
            0 -> SetupScreen(tick)
            1 -> PairScreen(onPaired = { tab = 2 }, pairLink = pairLink, onPairLinkHandled = onPairLinkHandled)
            else -> StatusScreen(tick)
        }
    }
}
