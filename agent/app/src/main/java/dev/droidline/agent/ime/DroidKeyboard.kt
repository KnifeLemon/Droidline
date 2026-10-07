package dev.droidline.agent.ime

import android.content.ClipboardManager
import android.content.ComponentName
import android.content.Context
import android.inputmethodservice.InputMethodService
import android.provider.Settings
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.ExtractedTextRequest
import android.view.inputmethod.InputMethodManager
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import dev.droidline.agent.Agent
import dev.droidline.agent.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Minimal keyboard that lets the agent send key codes, commit text and read the clipboard. */
class DroidKeyboard : InputMethodService() {
    override fun onCreate() {
        super.onCreate()
        instance = this
        Agent.refreshReady()
    }

    override fun onDestroy() {
        if (instance === this) instance = null
        super.onDestroy()
        Agent.refreshReady()
    }

    override fun onStartInput(attribute: EditorInfo?, restarting: Boolean) {
        super.onStartInput(attribute, restarting)
        Agent.refreshReady()
    }

    override fun onCreateInputView(): View {
        val pad = (8 * resources.displayMetrics.density).toInt()
        return LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(pad * 2, pad, pad, pad)
            addView(TextView(context).apply {
                text = getString(R.string.ime_bar_label)
                textSize = 14f
            }, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
            addView(Button(context).apply {
                text = getString(R.string.ime_switch)
                setOnClickListener { getSystemService(InputMethodManager::class.java).showInputMethodPicker() }
            })
        }
    }

    suspend fun sendKey(code: Int): Boolean = onMain {
        if (currentInputConnection == null) return@onMain false
        sendDownUpKeyEvents(code)
        true
    }

    suspend fun commit(text: String): Boolean = onMain {
        currentInputConnection?.commitText(text, 1) ?: false
    }

    /** Selects everything in the focused field and replaces it; an empty [text] clears the field. */
    suspend fun replaceAll(text: String): Boolean = onMain {
        val ic = currentInputConnection ?: return@onMain false
        ic.beginBatchEdit()
        try {
            ic.performContextMenuAction(android.R.id.selectAll)
            if (text.isEmpty()) ic.commitText("", 1) else ic.commitText(text, 1)
        } finally {
            ic.endBatchEdit()
        }
    }

    suspend fun appendText(text: String): Boolean = onMain {
        val ic = currentInputConnection ?: return@onMain false
        val len = ic.getExtractedText(ExtractedTextRequest(), 0)?.text?.length
        if (len != null) ic.setSelection(len, len) else sendDownUpKeyEvents(KeyEvent.KEYCODE_MOVE_END)
        ic.commitText(text, 1)
    }

    /** Android 10+ lets only the current IME or the focused app read the clipboard. */
    suspend fun readClipboard(): String = onMain {
        val cm = getSystemService(ClipboardManager::class.java)
        val clip = cm.primaryClip ?: return@onMain ""
        if (clip.itemCount == 0) "" else clip.getItemAt(0).coerceToText(this).toString()
    }

    private suspend fun <T> onMain(block: () -> T): T = withContext(Dispatchers.Main) { block() }

    companion object {
        @Volatile var instance: DroidKeyboard? = null
            private set

        fun component(context: Context) = ComponentName(context, DroidKeyboard::class.java)

        fun isEnabled(context: Context): Boolean = runCatching {
            val me = component(context)
            context.getSystemService(InputMethodManager::class.java).enabledInputMethodList.any { it.component == me }
        }.getOrDefault(false)

        fun isSelected(context: Context): Boolean {
            val id = Settings.Secure.getString(context.contentResolver, Settings.Secure.DEFAULT_INPUT_METHOD) ?: return false
            return ComponentName.unflattenFromString(id) == component(context)
        }

        /** The keyboard when it is the selected input method and running, else null. */
        fun current(context: Context): DroidKeyboard? = instance?.takeIf { isSelected(context) }
    }
}
