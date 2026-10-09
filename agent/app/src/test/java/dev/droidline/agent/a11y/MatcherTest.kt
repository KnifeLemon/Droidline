package dev.droidline.agent.a11y

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class MatcherTest {
    private val login = UiNode(text = "로그인", id = "com.kakao.talk:id/login", cls = "android.widget.TextView", bounds = Bounds(100, 900, 300, 960))
    private val loginRow = UiNode(cls = "android.widget.LinearLayout", clickable = true, bounds = Bounds(0, 880, 1080, 980), children = listOf(login))
    private val email = UiNode(cls = "android.widget.EditText", id = "com.kakao.talk:id/email", editable = true, text = "", bounds = Bounds(0, 400, 1080, 480))
    private val emailWrap = UiNode(cls = "com.google.android.material.textfield.TextInputLayout", children = listOf(UiNode(cls = "android.widget.FrameLayout", children = listOf(email))))
    private val box1 = UiNode(cls = "android.widget.CheckBox", desc = "자동 로그인", checkable = true, checked = true)
    private val box2 = UiNode(cls = "android.widget.CheckBox", desc = "알림 받기", checkable = true)
    private val list = UiNode(cls = "androidx.recyclerview.widget.RecyclerView", scrollable = true, bounds = Bounds(0, 200, 1080, 2000), children = listOf(box1, box2))
    private val small = UiNode(cls = "android.widget.ScrollView", scrollable = true, bounds = Bounds(0, 0, 100, 100))
    private val root = UiNode(cls = "android.widget.FrameLayout", pkg = "com.kakao.talk", children = listOf(emailWrap, loginRow, list, small))
    private val dialog = UiNode(cls = "android.widget.FrameLayout", children = listOf(UiNode(text = "확인", id = "android:id/button1", clickable = true)))
    private val roots = listOf(root)

    @Test
    fun textIsExact() {
        assertSame(login, Matcher.nth(roots, Selector("text", "로그인"), 0))
        assertNull(Matcher.nth(roots, Selector("text", "로그"), 0))
    }

    @Test
    fun textContainsIsSubstring() {
        assertSame(login, Matcher.nth(roots, Selector("textContains", "그인"), 0))
    }

    @Test
    fun idMatchesExactOrBareSuffix() {
        assertSame(login, Matcher.nth(roots, Selector("id", "com.kakao.talk:id/login"), 0))
        assertSame(login, Matcher.nth(roots, Selector("id", "login"), 0))
        assertNull(Matcher.nth(roots, Selector("id", "ogin"), 0))
        assertNull(Matcher.nth(roots, Selector("id", "other.pkg:id/login"), 0))
    }

    @Test
    fun descAndDescContains() {
        assertSame(box1, Matcher.nth(roots, Selector("desc", "자동 로그인"), 0))
        assertSame(box2, Matcher.nth(roots, Selector("descContains", "알림"), 0))
    }

    @Test
    fun classWithNthFollowsDocumentOrder() {
        val sel = Selector("class", "android.widget.CheckBox")
        assertSame(box1, Matcher.nth(roots, sel, 0))
        assertSame(box2, Matcher.nth(roots, sel, 1))
        assertNull(Matcher.nth(roots, sel, 2))
        assertEquals(2, Matcher.count(roots, sel))
    }

    @Test
    fun earlierRootsComeFirst() {
        val sel = Selector("class", "android.widget.FrameLayout")
        assertSame(dialog, Matcher.nth(listOf(dialog, root), sel, 0))
    }

    @Test
    fun unknownSelectorMatchesNothing() {
        assertEquals(0, Matcher.count(roots, Selector("bogus", "x")))
    }

    @Test
    fun clickableAncestorAndEditableDescendant() {
        assertSame(loginRow, Matcher.clickableAncestor(login))
        assertNull(Matcher.clickableAncestor(root))
        assertSame(email, Matcher.editableTarget(emailWrap))
        assertSame(email, Matcher.editableTarget(email))
        assertSame(login, Matcher.editableTarget(login))
    }

    @Test
    fun mainScrollableIsTheLargest() {
        assertSame(list, Matcher.mainScrollable(roots))
    }

    @Test
    fun describeFormatsTarget() {
        assertEquals("text '로그인'", Selector("text", "로그인").describe())
    }

    @Test
    fun dumpJsonHasExactlyTheNodeFields() {
        val o = root.toJson()
        val expected = setOf(
            "text", "id", "desc", "class", "package", "bounds", "clickable", "long_clickable", "checkable", "checked",
            "enabled", "focused", "selected", "scrollable", "editable", "password", "visible", "children",
        )
        assertEquals(expected, o.keys().asSequence().toSet())
        val loginJson = o.getJSONArray("children").getJSONObject(1).getJSONArray("children").getJSONObject(0)
        assertEquals("[100,900,300,960]", loginJson.getJSONArray("bounds").toString())
    }

    @Test
    fun passwordTextIsNotDumped() {
        val pw = UiNode(text = "secret", password = true, editable = true)
        assertEquals("", pw.toJson().getString("text"))
        assertTrue(pw.toJson().getBoolean("password"))
        assertFalse(pw.toJson().getBoolean("clickable"))
    }

    @Test
    fun directionSwipesUseTheMiddleSixtyPercent() {
        assertEquals(listOf(540, 1920, 540, 480), UiCommands.directionLine("up", 1080, 2400).toList())
        assertEquals(listOf(216, 1200, 864, 1200), UiCommands.directionLine("right", 1080, 2400).toList())
    }
}
