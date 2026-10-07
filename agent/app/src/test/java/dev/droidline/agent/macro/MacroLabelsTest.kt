package dev.droidline.agent.macro

import dev.droidline.agent.TestFiles
import dev.droidline.agent.a11y.UiNode
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class MacroLabelsTest {
    private val specs = MacroLabels.parse(TestFiles.asset("macro_labels.json").readText(Charsets.UTF_8))

    @Test
    fun everyStepTheMacrosUseIsPresent() {
        for (step in listOf("force_stop", "confirm", "storage", "clear_storage", "airplane", "wifi", "mobile_data")) {
            val s = specs[step]
            assertTrue("missing $step", s != null)
            assertTrue("$step has no labels", s!!.labels.isNotEmpty())
        }
    }

    @Test
    fun labelsFromAllLanguagesAreMerged() {
        val fs = specs.getValue("force_stop").labels
        assertTrue(fs.containsAll(listOf("Force stop", "강제 중지", "强行停止", "结束运行")))
        val clear = specs.getValue("clear_storage").labels
        assertTrue(clear.containsAll(listOf("Clear storage", "Clear data", "데이터 삭제", "저장공간 지우기", "清除存储空间", "清除数据")))
        assertTrue(specs.getValue("airplane").labels.containsAll(listOf("Airplane mode", "Aeroplane mode", "Flight mode", "비행기 탑재 모드", "비행기 모드", "飞行模式")))
        assertTrue(specs.getValue("wifi").labels.containsAll(listOf("Wi-Fi", "Use Wi-Fi", "Wi-Fi 사용", "WLAN", "使用 WLAN")))
        assertTrue(specs.getValue("mobile_data").labels.containsAll(listOf("Mobile data", "모바일 데이터", "移动数据")))
        assertTrue(specs.getValue("storage").labels.containsAll(listOf("Storage", "Storage & cache", "저장공간", "存储")))
        assertEquals(listOf("android:id/button1"), specs.getValue("confirm").ids)
    }

    @Test
    fun trustedIdWinsOverLabel() {
        val byLabel = UiNode(text = "OK")
        val byId = UiNode(text = "Delete", id = "android:id/button1")
        val root = UiNode(children = listOf(byLabel, byId))
        assertSame(byId, MacroLabels.find(listOf(root), specs.getValue("confirm")))
    }

    @Test
    fun genericIdNeedsALabelToo() {
        val disable = UiNode(text = "Disable", id = "com.android.settings:id/button3", clickable = true)
        assertNull(MacroLabels.find(listOf(UiNode(children = listOf(disable))), specs.getValue("force_stop")))
        val forceStop = UiNode(text = "강제 중지", id = "com.android.settings:id/button3", clickable = true)
        assertSame(forceStop, MacroLabels.find(listOf(UiNode(children = listOf(disable, forceStop))), specs.getValue("force_stop")))
    }

    @Test
    fun labelsMatchIgnoringCaseAndInListOrder() {
        val storage = UiNode(text = "Storage")
        val cache = UiNode(text = "storage & cache")
        val root = UiNode(children = listOf(storage, cache))
        assertSame(cache, MacroLabels.find(listOf(root), specs.getValue("storage")))
    }

    @Test
    fun descriptionIsUsedWhenTextIsMissing() {
        val n = UiNode(desc = "飞行模式")
        assertSame(n, MacroLabels.find(listOf(UiNode(children = listOf(n))), specs.getValue("airplane")))
    }

    @Test
    fun toggleIsFoundInTheRowButNotInOtherRows() {
        val title = UiNode(text = "Airplane mode")
        val sw = UiNode(id = "android:id/switch_widget", checkable = true, checked = false)
        val row = UiNode(clickable = true, children = listOf(UiNode(children = listOf(title)), sw))
        val otherSw = UiNode(checkable = true, checked = true)
        val otherRow = UiNode(clickable = true, children = listOf(UiNode(text = "Hotspot"), otherSw))
        val list = UiNode(scrollable = true, children = listOf(otherRow, row))
        val target = MacroLabels.find(listOf(list), specs.getValue("airplane"))!!
        assertSame(sw, MacroLabels.toggleOf(target))
        val lonely = UiNode(text = "Airplane mode")
        UiNode(scrollable = true, children = listOf(lonely, otherRow))
        assertNull(MacroLabels.toggleOf(lonely))
    }
}
