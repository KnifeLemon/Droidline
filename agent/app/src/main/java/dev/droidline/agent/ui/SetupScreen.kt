package dev.droidline.agent.ui

import android.Manifest
import android.net.VpnService
import android.os.Build
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.droidline.agent.Agent
import dev.droidline.agent.R
import dev.droidline.agent.device.Locate
import dev.droidline.agent.device.Phone
import dev.droidline.agent.ime.DroidKeyboard
import dev.droidline.agent.notif.DroidNotificationListener

@Composable
fun SetupScreen(tick: Int) {
    val ctx = LocalContext.current
    val ready by Agent.ready.collectAsState()
    var localTick by remember { mutableIntStateOf(0) }
    val notifPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { localTick++ }
    val vpnConsent = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        Agent.refreshReady()
        localTick++
    }
    // Re-read on every resume and after each permission result.
    val key = tick + localTick
    val imeEnabled = remember(key) { DroidKeyboard.isEnabled(ctx) }
    val imeSelected = remember(key) { DroidKeyboard.isSelected(ctx) }
    val canNotify = remember(key) { SystemScreens.canPostNotifications(ctx) }
    val batteryFree = remember(key) { SystemScreens.ignoringBatteryOptimizations(ctx) }
    val listenerOn = remember(key) { DroidNotificationListener.isEnabled(ctx) }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(stringResource(R.string.setup_intro), style = MaterialTheme.typography.bodyMedium)

        StepCard(
            title = stringResource(R.string.a11y_title),
            body = stringResource(R.string.a11y_desc),
            state = if (ready.a11y) StepState.ON else StepState.OFF,
            note = if (Build.VERSION.SDK_INT >= 33 && !ready.a11y) stringResource(R.string.a11y_restricted) else null,
        ) {
            Button(onClick = { SystemScreens.accessibility(ctx) }) { Text(stringResource(R.string.btn_open_settings)) }
            if (Build.VERSION.SDK_INT >= 33 && !ready.a11y) {
                OutlinedButton(onClick = { SystemScreens.appInfo(ctx) }) { Text(stringResource(R.string.btn_app_info)) }
            }
        }

        StepCard(
            title = stringResource(R.string.ime_title),
            body = stringResource(R.string.ime_desc),
            state = when {
                imeEnabled && imeSelected -> StepState.ON
                imeEnabled -> StepState.PARTIAL
                else -> StepState.OFF
            },
            partialText = stringResource(R.string.ime_enabled_not_selected),
        ) {
            if (!imeEnabled) Button(onClick = { SystemScreens.keyboardSettings(ctx) }) { Text(stringResource(R.string.btn_enable)) }
            else OutlinedButton(onClick = { SystemScreens.keyboardSettings(ctx) }) { Text(stringResource(R.string.btn_open_settings)) }
            if (imeEnabled && !imeSelected) Button(onClick = { SystemScreens.keyboardPicker(ctx) }) { Text(stringResource(R.string.btn_select)) }
        }

        if (Build.VERSION.SDK_INT >= 33) {
            StepCard(
                title = stringResource(R.string.post_notif_title),
                body = stringResource(R.string.post_notif_desc),
                state = if (canNotify) StepState.ON else StepState.OFF,
            ) {
                if (!canNotify) Button(onClick = { notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS) }) {
                    Text(stringResource(R.string.btn_allow))
                }
            }
        }

        StepCard(
            title = stringResource(R.string.battery_title),
            body = stringResource(R.string.battery_desc),
            state = if (batteryFree) StepState.ON else StepState.OFF,
            onText = stringResource(R.string.battery_on),
        ) {
            if (!batteryFree) Button(onClick = { SystemScreens.batteryOptimization(ctx) }) { Text(stringResource(R.string.btn_open_settings)) }
        }

        StepCard(
            title = stringResource(R.string.notif_access_title),
            body = stringResource(R.string.notif_access_desc),
            state = if (listenerOn) StepState.ON else StepState.OPTIONAL,
        ) {
            OutlinedButton(onClick = { SystemScreens.notificationAccess(ctx) }) { Text(stringResource(R.string.btn_open_settings)) }
        }

        StepCard(
            title = stringResource(R.string.vpn_title),
            body = stringResource(R.string.vpn_desc),
            state = if (ready.vpn) StepState.ON else StepState.OPTIONAL,
        ) {
            if (!ready.vpn) OutlinedButton(onClick = {
                val intent = VpnService.prepare(ctx)
                if (intent != null) vpnConsent.launch(intent) else Agent.refreshReady()
            }) { Text(stringResource(R.string.btn_grant)) }
        }
        DataCard(R.string.sms_title, R.string.sms_desc, R.string.sms_off_note, arrayOf(Manifest.permission.READ_SMS), ready.sms) { localTick++ }
        DataCard(R.string.sms_send_title, R.string.sms_send_desc, R.string.sms_send_off_note, arrayOf(Manifest.permission.SEND_SMS), ready.smsSend) { localTick++ }
        DataCard(R.string.calls_title, R.string.calls_desc, R.string.calls_off_note, arrayOf(Manifest.permission.READ_CALL_LOG), ready.calls) { localTick++ }
        DataCard(R.string.phone_title, R.string.phone_desc, R.string.phone_off_note, Phone.permissions, ready.phone) { localTick++ }
        DataCard(R.string.contacts_title, R.string.contacts_desc, R.string.contacts_off_note, arrayOf(Manifest.permission.READ_CONTACTS), ready.contacts) { localTick++ }
        FilesCard(ready.files) { localTick++ }
        LocationCard(ready.location, Locate.foreground(ctx)) { localTick++ }
        Spacer(Modifier.height(8.dp))
    }
}

/** Asks for runtime permissions; after "Don't ask again" the dialog no longer shows, so it opens App info instead. */
@Composable
private fun rememberAsk(changed: () -> Unit): (Array<String>) -> Unit {
    val ctx = LocalContext.current
    var asked by remember { mutableStateOf(emptyArray<String>()) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { result ->
        val activity = ctx as? ComponentActivity
        val denied = asked.filter { result[it] == false }
        if (denied.isNotEmpty() && activity != null && denied.none { activity.shouldShowRequestPermissionRationale(it) }) {
            SystemScreens.appInfo(ctx)
        }
        Agent.refreshReady()
        changed()
    }
    return { perms -> asked = perms; launcher.launch(perms) }
}

/** An optional permission for personal data, asked for only when the user taps Allow. */
@Composable
private fun DataCard(title: Int, body: Int, offNote: Int, permissions: Array<String>, on: Boolean, changed: () -> Unit) {
    val ctx = LocalContext.current
    val ask = rememberAsk(changed)
    StepCard(
        title = stringResource(title),
        body = stringResource(body),
        state = if (on) StepState.ON else StepState.OPTIONAL,
        note = if (on) stringResource(offNote) else null,
    ) {
        if (!on) OutlinedButton(onClick = { ask(permissions) }) { Text(stringResource(R.string.btn_allow)) }
        else OutlinedButton(onClick = { SystemScreens.appInfo(ctx) }) { Text(stringResource(R.string.btn_app_info)) }
    }
}

/** Android 11+ grants file access on its own settings page; older versions ask with a dialog. */
@Composable
private fun FilesCard(on: Boolean, changed: () -> Unit) {
    val ctx = LocalContext.current
    val ask = rememberAsk(changed)
    StepCard(
        title = stringResource(R.string.files_title),
        body = stringResource(R.string.files_desc),
        state = if (on) StepState.ON else StepState.OPTIONAL,
        note = if (on) stringResource(R.string.files_off_note) else null,
    ) {
        OutlinedButton(onClick = {
            when {
                Build.VERSION.SDK_INT >= 30 -> SystemScreens.allFilesAccess(ctx)
                on -> SystemScreens.appInfo(ctx)
                else -> ask(arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE))
            }
        }) { Text(stringResource(if (on || Build.VERSION.SDK_INT >= 30) R.string.btn_open_settings else R.string.btn_allow)) }
    }
}

/** Location needs two steps on Android 10+: while in use first, then all the time, which Android asks for separately. */
@Composable
private fun LocationCard(on: Boolean, foreground: Boolean, changed: () -> Unit) {
    val ctx = LocalContext.current
    val ask = rememberAsk(changed)
    StepCard(
        title = stringResource(R.string.location_title),
        body = stringResource(R.string.location_desc),
        state = when {
            on -> StepState.ON
            foreground -> StepState.PARTIAL
            else -> StepState.OPTIONAL
        },
        partialText = stringResource(R.string.location_partial),
        note = if (on) stringResource(R.string.location_off_note) else null,
    ) {
        when {
            on -> OutlinedButton(onClick = { SystemScreens.appInfo(ctx) }) { Text(stringResource(R.string.btn_app_info)) }
            !foreground -> OutlinedButton(onClick = {
                ask(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION))
            }) { Text(stringResource(R.string.btn_allow)) }
            else -> Button(onClick = { ask(arrayOf(Manifest.permission.ACCESS_BACKGROUND_LOCATION)) }) {
                Text(stringResource(R.string.btn_allow_always))
            }
        }
    }
}

enum class StepState { ON, OFF, PARTIAL, OPTIONAL }

@Composable
fun StepCard(
    title: String,
    body: String,
    state: StepState,
    note: String? = null,
    onText: String? = null,
    partialText: String? = null,
    actions: @Composable () -> Unit,
) {
    Card(
        Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(horizontalArrangement = Arrangement.SpaceBetween, modifier = Modifier.fillMaxWidth()) {
                Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                val (label, color) = when (state) {
                    StepState.ON -> (onText ?: stringResource(R.string.step_on)) to MaterialTheme.colorScheme.primary
                    StepState.OFF -> stringResource(R.string.step_off) to MaterialTheme.colorScheme.error
                    StepState.PARTIAL -> (partialText ?: stringResource(R.string.step_off)) to MaterialTheme.colorScheme.error
                    StepState.OPTIONAL -> stringResource(R.string.step_optional) to MaterialTheme.colorScheme.onSurfaceVariant
                }
                Text(label, color = color, style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold)
            }
            Text(body, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (note != null) Text(note, style = MaterialTheme.typography.bodySmall)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) { actions() }
        }
    }
}
