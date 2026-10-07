package dev.droidline.agent.ui

import android.content.Context
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import dev.droidline.agent.Agent
import dev.droidline.agent.R
import dev.droidline.agent.link.DiscoveredServer
import dev.droidline.agent.link.Discovery
import dev.droidline.agent.link.LinkTarget
import dev.droidline.agent.link.Phase
import dev.droidline.agent.link.QrUri
import dev.droidline.agent.service.AgentService
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Starts the service if needed and hands it the pairing target once it is up. */
private suspend fun beginPairing(ctx: Context, target: LinkTarget) {
    AgentService.start(ctx)
    repeat(60) {
        Agent.link?.let {
            it.startPairing(target)
            return
        }
        delay(50)
    }
}

@Composable
fun PairScreen(onPaired: () -> Unit, pairLink: String? = null, onPairLinkHandled: () -> Unit = {}) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val status by Agent.status.collectAsState()
    val pairFailure by Agent.pairFailure.collectAsState()
    var qrError by remember { mutableStateOf<String?>(null) }
    var searching by remember { mutableStateOf(false) }
    var found by remember { mutableStateOf<List<DiscoveredServer>?>(null) }
    var started by remember { mutableStateOf(false) }
    val record = remember(status) { Agent.pairing.load() }

    val scan = rememberLauncherForActivityResult(ScanContract()) { result ->
        val text = result.contents ?: return@rememberLauncherForActivityResult
        val qr = runCatching { QrUri.parse(text) }.getOrNull()
        if (qr == null) {
            qrError = ctx.getString(R.string.qr_invalid)
        } else {
            qrError = null
            found = null
            started = true
            scope.launch { beginPairing(ctx, LinkTarget.PairQr(qr)) }
        }
    }

    val linked = remember(pairLink) { pairLink?.let { runCatching { QrUri.parse(it) }.getOrNull() } }
    LaunchedEffect(pairLink) {
        if (pairLink != null && linked == null) {
            qrError = ctx.getString(R.string.qr_invalid)
            onPairLinkHandled()
        }
    }

    LaunchedEffect(status.phase, status.pairing) {
        if (started && status.phase == Phase.CONNECTED && !status.pairing) {
            started = false
            onPaired()
        }
    }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(stringResource(R.string.pair_intro), style = MaterialTheme.typography.bodyMedium)
        if (linked != null) {
            // A link can come from anywhere, so the person confirms which PC gets control.
            Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(stringResource(R.string.pair_link_title, linked.serverName), style = MaterialTheme.typography.titleMedium)
                    Text(linked.addresses.joinToString("\n"), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                    Text(stringResource(R.string.pair_link_warning), style = MaterialTheme.typography.bodySmall)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Button(onClick = {
                            qrError = null
                            found = null
                            started = true
                            onPairLinkHandled()
                            scope.launch { beginPairing(ctx, LinkTarget.PairQr(linked)) }
                        }) { Text(stringResource(R.string.btn_pair_confirm)) }
                        OutlinedButton(onClick = onPairLinkHandled) { Text(stringResource(R.string.btn_cancel)) }
                    }
                }
            }
        }
        if (record != null) {
            Text(stringResource(R.string.pair_replace_note, record.serverName), style = MaterialTheme.typography.bodySmall)
        }

        val busy = status.pairing
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(enabled = !busy, onClick = {
                scan.launch(
                    ScanOptions()
                        .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                        .setPrompt(ctx.getString(R.string.scan_prompt))
                        .setBeepEnabled(false)
                        .setOrientationLocked(false)
                )
            }) { Text(stringResource(R.string.btn_scan_qr)) }
            OutlinedButton(enabled = !busy && !searching, onClick = {
                searching = true
                found = null
                scope.launch {
                    found = withContext(Dispatchers.IO) { Discovery.run(ctx, null, listenMs = 2500, rounds = 3) }
                    searching = false
                }
            }) { Text(stringResource(R.string.btn_pair_wifi)) }
        }
        qrError?.let { Text(it, color = MaterialTheme.colorScheme.error) }

        if (searching) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                CircularProgressIndicator(Modifier.padding(4.dp))
                Text(stringResource(R.string.searching))
            }
        }

        found?.let { list ->
            if (list.isEmpty()) {
                Text(stringResource(R.string.no_pc_found))
            } else if (!busy) {
                Text(stringResource(R.string.pick_pc), style = MaterialTheme.typography.titleMedium)
                list.forEach { s ->
                    Card(
                        Modifier.fillMaxWidth().clickable(enabled = s.pairing) {
                            started = true
                            scope.launch { beginPairing(ctx, LinkTarget.PairCode(s)) }
                        },
                        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
                    ) {
                        Column(Modifier.padding(16.dp)) {
                            Text(s.name, style = MaterialTheme.typography.titleMedium)
                            Text("${s.host}:${s.port}", style = MaterialTheme.typography.bodySmall)
                            if (!s.pairing) {
                                Text(stringResource(R.string.pc_pairing_closed), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                            }
                        }
                    }
                }
            }
        }

        if (status.pairing) PairProgress(status.phase, status.sas, status.serverName)
        if (!status.pairing && pairFailure != null) {
            Text(stringResource(R.string.pair_failed, pairError(pairFailure)), color = MaterialTheme.colorScheme.error)
        }
        if (busy) OutlinedButton(onClick = { Agent.link?.cancelPairing() }) { Text(stringResource(R.string.btn_cancel)) }
        if (!status.pairing && status.phase == Phase.CONNECTED && record != null) {
            Text(stringResource(R.string.pair_done, record.serverName), color = MaterialTheme.colorScheme.primary)
        }
    }
}

@Composable
private fun PairProgress(phase: Phase, sas: String?, serverName: String?) {
    when (phase) {
        Phase.WAITING_CODE -> Card(
            Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        ) {
            Column(Modifier.padding(20.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(10.dp)) {
                serverName?.let { Text(it, style = MaterialTheme.typography.titleMedium) }
                Text(
                    (sas ?: "").chunked(3).joinToString(" "),
                    fontSize = 48.sp,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.Bold,
                    letterSpacing = 4.sp,
                )
                Text(stringResource(R.string.code_instruction))
                SelectionContainer {
                    Text("droidline pair ${sas ?: ""}", fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.titleMedium)
                }
                Text(stringResource(R.string.code_wait), style = MaterialTheme.typography.bodySmall)
            }
        }
        else -> Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            CircularProgressIndicator(Modifier.padding(4.dp))
            Text(stringResource(R.string.pairing_connecting))
        }
    }
}

@Composable
private fun pairError(detail: String?): String = when (detail) {
    "pairing_closed" -> stringResource(R.string.pair_err_pairing_closed)
    "unreachable" -> stringResource(R.string.pair_err_unreachable)
    "code_expired" -> stringResource(R.string.pair_err_code_expired)
    null -> stringResource(R.string.pair_err_unreachable)
    else -> detail
}
