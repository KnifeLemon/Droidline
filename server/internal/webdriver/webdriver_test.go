package webdriver

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestUiSelector(t *testing.T) {
	cases := []struct {
		src  string
		want string
		nth  int
		scr  bool
	}{
		{`new UiSelector().text("OK").clickable(true)`, `map[clickable:true text:OK]`, 0, false},
		{`new UiSelector().resourceId("com.app:id/login").instance(2);`, `map[id:com.app:id/login]`, 2, false},
		{`new UiSelector().textStartsWith("Wi").className("android.widget.TextView")`, `map[class:android.widget.TextView textMatches:Wi.*]`, 0, false},
		{`new UiSelector().className("android.widget.LinearLayout").childSelector(new UiSelector().text("Wi-Fi"))`, `map[inside:map[class:android.widget.LinearLayout] text:Wi-Fi]`, 0, false},
		{`new UiScrollable(new UiSelector().scrollable(true)).scrollIntoView(new UiSelector().text("About phone"))`, `map[text:About phone]`, 0, true},
		{`new UiScrollable(new UiSelector().scrollable(true)).scrollTextIntoView("Battery")`, `map[text:Battery]`, 0, true},
		{`new UiSelector().description("Navigate \"up\"")`, `map[desc:Navigate "up"]`, 0, false},
	}
	for _, c := range cases {
		got, err := parseUiSelector(c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if fmt.Sprint(got.query) != c.want || got.instance != c.nth || got.scroll != c.scr {
			t.Errorf("%s: got %v nth=%d scroll=%v", c.src, got.query, got.instance, got.scroll)
		}
	}
	for _, bad := range []string{`new UiSelector()`, `new UiSelector().index(2)`, `UiSelector().text("x")`, `new UiSelector().text("x"`} {
		if _, err := parseUiSelector(bad); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}

func TestCSSQuery(t *testing.T) {
	for in, want := range map[string]string{
		`#login`:                 `map[id:login]`,
		`[id="com.app:id/ok"]`:   `map[id:com.app:id/ok]`,
		`*[name='Sign in']`:      `map[text:Sign in]`,
		`.android.widget.Button`: `map[class:android.widget.Button]`,
	} {
		got, err := cssQuery(in)
		if err != nil || fmt.Sprint(got) != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	if _, err := cssQuery(`div > p`); err == nil {
		t.Error("accepted a CSS selector it cannot map")
	}
}

func testDoc(t *testing.T) *xnode {
	raw, err := os.ReadFile("../../../spec/query-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Tree map[string]any `json:"tree"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return document(map[string]any{"width": 1080, "height": 2400, "tree": v.Tree})
}

func TestXPathAndSource(t *testing.T) {
	doc := testDoc(t)
	src := pageSource(doc)
	for _, want := range []string{`<hierarchy index="0" class="hierarchy"`, `<android.widget.Switch index="1"`, `text="Wi-Fi"`, `bounds="[900,360][1040,440]"`, `checked="true"`} {
		if !strings.Contains(src, want) {
			t.Errorf("page source lacks %s", want)
		}
	}
	cases := map[string]int{
		`//android.widget.TextView[@text='Wi-Fi']`:                                                  1,
		`//*[@resource-id='android:id/title']`:                                                      3,
		`//*[contains(@text, '저장')]`:                                                                2,
		`//*[@text='Bluetooth']/../following-sibling::android.widget.Switch`:                        1,
		`(//android.widget.Button)[2]`:                                                              1,
		`//android.widget.LinearLayout[.//*[@text='Wi-Fi']]/android.widget.Switch[@checked='true']`: 1,
		`//*[@text='없음']`:                                                                           0,
		`//*[@displayed='false']`:                                                                   1,
	}
	for expr, n := range cases {
		got, err := selectXPath(doc, doc, expr)
		if err != nil || len(got) != n {
			t.Errorf("%s: %d matches, %v", expr, len(got), err)
		}
	}
	got, _ := selectXPath(doc, doc, `(//android.widget.Button)[2]`)
	if len(got) == 1 && got[0]["text"] != "확인" {
		t.Errorf("second button = %v", got[0]["text"])
	}
	row := doc.find(map[string]any{"bounds": []any{float64(0), float64(500), float64(1080), float64(700)}, "class": "android.widget.LinearLayout"})
	if row == nil {
		t.Fatal("row not found by bounds")
	}
	inRow, _ := selectXPath(doc, row, `.//*[@resource-id='android:id/summary']`)
	if len(inRow) != 1 || inRow[0]["text"] != "사용 안함" {
		t.Errorf("relative xpath = %v", inRow)
	}
	if _, err := selectXPath(doc, doc, `//*[`); err == nil {
		t.Error("broken xpath accepted")
	}
}
