package dev.droidline.agent.ime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class KeyNamesTest {
    @Test
    fun tableMatchesTheSpec() {
        val expected = mapOf(
            "enter" to 66, "tab" to 61, "del" to 67, "backspace" to 67, "forward_del" to 112, "space" to 62,
            "escape" to 111, "up" to 19, "down" to 20, "left" to 21, "right" to 22, "search" to 84, "menu" to 82,
            "page_up" to 92, "page_down" to 93, "move_home" to 122, "move_end" to 123,
        )
        assertEquals(expected, KeyNames.CODES)
        for ((name, code) in expected) assertEquals(KeyAction.Code(code), KeyNames.resolve(name, null))
    }

    @Test
    fun systemKeysAreGlobalActions() {
        for (k in listOf("back", "home", "recents", "notifications", "quick_settings", "lock")) {
            assertEquals(KeyAction.Global(k), KeyNames.resolve(k, null))
        }
        assertEquals(KeyAction.Global("back"), KeyNames.resolve("BACK", null))
    }

    @Test
    fun numbersAreKeyCodes() {
        assertEquals(KeyAction.Code(66), KeyNames.resolve(66, null))
        assertEquals(KeyAction.Code(66), KeyNames.resolve("66", null))
        assertNull(KeyNames.resolve(0, null))
        assertNull(KeyNames.resolve(-3, null))
    }

    @Test
    fun unknownWordsAreTypedAsText() {
        assertEquals(KeyAction.Text("안녕"), KeyNames.resolve("안녕", null))
        assertEquals(KeyAction.Text("hello world"), KeyNames.resolve("hello world", null))
    }

    @Test
    fun textParamIsAlwaysLiteral() {
        assertEquals(KeyAction.Text("enter"), KeyNames.resolve(null, "enter"))
        assertEquals(KeyAction.Text("back"), KeyNames.resolve("home", "back"))
    }

    @Test
    fun nothingToSend() {
        assertNull(KeyNames.resolve(null, null))
        assertNull(KeyNames.resolve("", ""))
    }
}
