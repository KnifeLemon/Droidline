package dev.droidline.agent.ime

/** How sendkey interprets its `key` and `text` parameters. */
sealed class KeyAction {
    /** An accessibility global action such as back or home. */
    data class Global(val name: String) : KeyAction()

    /** An Android key code sent through the Droidline keyboard. */
    data class Code(val code: Int) : KeyAction()

    /** Text committed through the Droidline keyboard. */
    data class Text(val text: String) : KeyAction()
}

object KeyNames {
    val CODES: Map<String, Int> = mapOf(
        "enter" to 66,
        "tab" to 61,
        "del" to 67,
        "backspace" to 67,
        "forward_del" to 112,
        "space" to 62,
        "escape" to 111,
        "up" to 19,
        "down" to 20,
        "left" to 21,
        "right" to 22,
        "search" to 84,
        "menu" to 82,
        "page_up" to 92,
        "page_down" to 93,
        "move_home" to 122,
        "move_end" to 123,
    )

    val GLOBAL: Set<String> = setOf("back", "home", "recents", "notifications", "quick_settings", "lock")

    private const val MAX_KEY_CODE = 999

    /** Returns null when neither parameter carries anything to send. */
    fun resolve(key: Any?, text: String?): KeyAction? {
        if (!text.isNullOrEmpty()) return KeyAction.Text(text)
        return when (key) {
            null -> null
            is Number -> code(key.toLong())
            is String -> {
                if (key.isEmpty()) return null
                val lower = key.lowercase()
                when {
                    lower in GLOBAL -> KeyAction.Global(lower)
                    lower in CODES -> KeyAction.Code(CODES.getValue(lower))
                    key.all { it.isDigit() } && key.length <= 4 -> code(key.toLong())
                    else -> KeyAction.Text(key)
                }
            }
            else -> null
        }
    }

    private fun code(n: Long): KeyAction? = if (n in 1..MAX_KEY_CODE) KeyAction.Code(n.toInt()) else null
}
