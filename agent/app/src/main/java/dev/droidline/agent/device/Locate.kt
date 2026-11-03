package dev.droidline.agent.device

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.pm.PackageManager
import android.location.Location
import android.location.LocationListener
import android.location.LocationManager
import android.location.LocationRequest
import android.os.Build
import android.os.CancellationSignal
import android.os.Looper
import androidx.core.content.ContextCompat
import dev.droidline.agent.cmd.CmdError
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import kotlin.coroutines.resume

/** The phone's location. Needs fine location and, on Android 10+, background location, because commands run in the background. */
object Locate {
    private fun has(ctx: Context, perm: String) =
        ContextCompat.checkSelfPermission(ctx, perm) == PackageManager.PERMISSION_GRANTED

    fun foreground(ctx: Context) = has(ctx, Manifest.permission.ACCESS_FINE_LOCATION)

    fun granted(ctx: Context) =
        foreground(ctx) && (Build.VERSION.SDK_INT < 29 || has(ctx, Manifest.permission.ACCESS_BACKGROUND_LOCATION))

    @SuppressLint("MissingPermission")
    suspend fun read(ctx: Context, p: JSONObject): JSONObject {
        if (!granted(ctx)) throw CmdError.noPermission("location")
        val timeout = p.optDouble("timeout", 10.0)
        val maxAge = p.optDouble("max_age", 60.0)
        if (timeout <= 0 || timeout > 300) throw CmdError.badArgs("location", "timeout must be above 0 and at most 300")
        if (maxAge < 0) throw CmdError.badArgs("location", "max_age must be 0 or more")
        val lm = ctx.getSystemService(LocationManager::class.java)
        if (!lm.isLocationEnabled) throw CmdError("LOCATION_OFF")
        val providers = lm.getProviders(true)
        if (providers.isEmpty()) throw CmdError("LOCATION_OFF")

        if (maxAge > 0) {
            val oldest = System.currentTimeMillis() - (maxAge * 1000).toLong()
            providers.mapNotNull { lm.getLastKnownLocation(it) }
                .filter { it.time >= oldest }
                .minByOrNull { it.accuracy }
                ?.let { return json(it) }
        }

        val provider = listOf(fused(), LocationManager.NETWORK_PROVIDER, LocationManager.GPS_PROVIDER)
            .firstOrNull { it != null && it in providers } ?: providers.first()
        val fix = withTimeoutOrNull((timeout * 1000).toLong()) { current(lm, provider) }
            ?: throw CmdError("TIMEOUT", mapOf("cmd" to "location", "timeout" to timeout))
        return json(fix)
    }

    private fun fused() = if (Build.VERSION.SDK_INT >= 31) LocationManager.FUSED_PROVIDER else null

    @SuppressLint("MissingPermission")
    private suspend fun current(lm: LocationManager, provider: String): Location = withContext(Dispatchers.Main) {
        suspendCancellableCoroutine { cont ->
            if (Build.VERSION.SDK_INT >= 31) {
                val cancel = CancellationSignal()
                cont.invokeOnCancellation { cancel.cancel() }
                // High accuracy lets the fused provider turn on GPS; balanced stays on Wi-Fi and cell, which may have nothing.
                val request = LocationRequest.Builder(0).setQuality(LocationRequest.QUALITY_HIGH_ACCURACY).build()
                lm.getCurrentLocation(provider, request, cancel, { it.run() }) { loc ->
                    if (loc != null) cont.resume(loc)
                }
            } else if (Build.VERSION.SDK_INT >= 30) {
                val cancel = CancellationSignal()
                cont.invokeOnCancellation { cancel.cancel() }
                lm.getCurrentLocation(provider, cancel, { it.run() }) { loc ->
                    if (loc != null) cont.resume(loc)
                }
            } else {
                val listener = object : LocationListener {
                    override fun onLocationChanged(loc: Location) {
                        lm.removeUpdates(this)
                        cont.resume(loc)
                    }
                }
                cont.invokeOnCancellation { lm.removeUpdates(listener) }
                @Suppress("DEPRECATION")
                lm.requestSingleUpdate(provider, listener, Looper.getMainLooper())
            }
        }
    }

    private fun json(l: Location) = JSONObject()
        .put("lat", l.latitude)
        .put("lon", l.longitude)
        .put("accuracy", l.accuracy.toDouble())
        .put("time", l.time)
        .put("provider", l.provider.orEmpty())
}
