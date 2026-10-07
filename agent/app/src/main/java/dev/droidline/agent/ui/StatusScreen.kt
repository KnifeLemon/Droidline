package dev.droidline.agent.ui

import android.content.Intent
import android.content.pm.ApplicationInfo
import android.net.Uri
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import dev.droidline.agent.Agent
import dev.droidline.agent.R
import dev.droidline.agent.link.LinkStatus
import dev.droidline.agent.link.Phase
import dev.droidline.agent.service.AgentService
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

@Composable
fun StatusScreen(tick: Int) {
    val ctx = LocalContext.current
    val status by Agent.status.collectAsState()
    val ready by Agent.ready.collectAsState()
    val revision by Agent.pairing.revision.collectAsState()
    val record = remember(status, tick, revision) { Agent.pairing.load() }
    var allowlist by remember(tick) { mutableStateOf(Agent.settings.notifyAllowlist) }
    var picking by remember { mutableStateOf(false) }
    var confirmUnpair by remember { mutableStateOf(false) }
    val latest = remember(tick) { Agent.settings.latestRelease }
    val latestUrl = remember(tick) { Agent.settings.latestReleaseUrl }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        if (latest != null && latestUrl != null) {
            Section {
                Text(stringResource(R.string.update_available, latest), style = MaterialTheme.typography.titleMedium)
                OutlinedButton(onClick = {
                    runCatching { ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(latestUrl)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
                }) { Text(stringResource(R.string.btn_release_page)) }
            }
        }

        Section {
            Text(stringResource(R.string.status_connection), style = MaterialTheme.typography.titleMedium)
            Text(phaseText(status), color = if (status.phase == Phase.CONNECTED) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface)
            if (record != null) InfoRow(stringResource(R.string.label_pc), record.serverName)
            status.route?.takeIf { status.phase == Phase.CONNECTED }?.let { InfoRow(stringResource(R.string.label_route), it) }
            InfoRow(stringResource(R.string.label_device_id), Agent.identity.deviceId, mono = true)
            record?.deviceName?.let { InfoRow(stringResource(R.string.label_device_name), it) }
            InfoRow(stringResource(R.string.agent_version_label), Agent.version)
            if (record != null) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = {
                        AgentService.start(ctx)
                        Agent.link?.reconnectNow()
                    }) { Text(stringResource(R.string.btn_reconnect)) }
                    TextButton(onClick = { confirmUnpair = true }) { Text(stringResource(R.string.btn_unpair), color = MaterialTheme.colorScheme.error) }
                }
            }
        }

        Section {
            Text(stringResource(R.string.label_ready), style = MaterialTheme.typography.titleMedium)
            ReadyRow(stringResource(R.string.ready_a11y), ready.a11y)
            ReadyRow(stringResource(R.string.ready_ime), ready.ime)
            ReadyRow(stringResource(R.string.ready_notif), ready.notif)
            ReadyRow(stringResource(R.string.ready_capture), ready.capture)
            ReadyRow(stringResource(R.string.ready_vpn), ready.vpn)
        }

        Section {
            Text(stringResource(R.string.notif_filter_title), style = MaterialTheme.typography.titleMedium)
            if (allowlist.isEmpty()) {
                Text(stringResource(R.string.notif_filter_empty), color = MaterialTheme.colorScheme.onSurfaceVariant)
            } else {
                allowlist.sorted().forEach { Text(it, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
            }
            OutlinedButton(onClick = { picking = true }) { Text(stringResource(R.string.btn_choose_apps)) }
        }
    }

    if (picking) {
        AppPicker(
            initial = allowlist,
            onDismiss = { picking = false },
            onDone = {
                Agent.settings.notifyAllowlist = it
                allowlist = it
                picking = false
            },
        )
    }

    if (confirmUnpair && record != null) {
        AlertDialog(
            onDismissRequest = { confirmUnpair = false },
            text = { Text(stringResource(R.string.unpair_confirm, record.serverName)) },
            confirmButton = {
                TextButton(onClick = {
                    confirmUnpair = false
                    Agent.pairing.clear()
                    Agent.session.resetForNewServer()
                    AgentService.stop(ctx)
                    Agent.status.value = LinkStatus()
                }) { Text(stringResource(R.string.btn_unpair)) }
            },
            dismissButton = { TextButton(onClick = { confirmUnpair = false }) { Text(stringResource(R.string.btn_cancel)) } },
        )
    }
}

@Composable
private fun phaseText(s: LinkStatus): String = when (s.phase) {
    Phase.CONNECTED -> stringResource(R.string.phase_connected)
    Phase.CONNECTING -> stringResource(R.string.phase_connecting)
    Phase.OFFLINE -> stringResource(R.string.phase_offline)
    Phase.WAITING_CODE -> stringResource(R.string.phase_waiting_code)
    Phase.REJECTED -> stringResource(R.string.phase_rejected)
    Phase.IDLE, Phase.PAIR_FAILED -> if (Agent.isPaired) stringResource(R.string.phase_offline) else stringResource(R.string.phase_idle)
}

@Composable
private fun Section(content: @Composable () -> Unit) {
    Card(Modifier.fillMaxWidth(), colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) { content() }
    }
}

@Composable
private fun InfoRow(label: String, value: String, mono: Boolean = false) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, fontFamily = if (mono) FontFamily.Monospace else FontFamily.Default)
    }
}

@Composable
private fun ReadyRow(label: String, on: Boolean) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label)
        Text(
            stringResource(if (on) R.string.step_on else R.string.step_off),
            color = if (on) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

private class AppRow(val pkg: String, val label: String)

@Composable
private fun AppPicker(initial: Set<String>, onDismiss: () -> Unit, onDone: (Set<String>) -> Unit) {
    val ctx = LocalContext.current
    var query by remember { mutableStateOf("") }
    var chosen by remember { mutableStateOf(initial) }
    val apps by produceState<List<AppRow>?>(null) {
        value = withContext(Dispatchers.IO) {
            val pm = ctx.packageManager
            pm.getInstalledApplications(0)
                .filter { it.flags and ApplicationInfo.FLAG_SYSTEM == 0 || pm.getLaunchIntentForPackage(it.packageName) != null }
                .map { AppRow(it.packageName, pm.getApplicationLabel(it).toString()) }
                .sortedBy { it.label.lowercase() }
        }
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.notif_filter_title)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = query,
                    onValueChange = { query = it },
                    label = { Text(stringResource(R.string.search_apps)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                val list = apps.orEmpty().filter {
                    query.isBlank() || it.label.contains(query, true) || it.pkg.contains(query, true)
                }
                LazyColumn(Modifier.heightIn(max = 380.dp)) {
                    items(list, key = { it.pkg }) { app ->
                        Row(
                            Modifier.fillMaxWidth().clickable {
                                chosen = if (app.pkg in chosen) chosen - app.pkg else chosen + app.pkg
                            },
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Checkbox(checked = app.pkg in chosen, onCheckedChange = null)
                            Column(Modifier.padding(start = 8.dp)) {
                                Text(app.label)
                                Text(app.pkg, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                    }
                }
            }
        },
        confirmButton = { Button(onClick = { onDone(chosen) }) { Text(stringResource(R.string.btn_done)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.btn_cancel)) } },
    )
}
