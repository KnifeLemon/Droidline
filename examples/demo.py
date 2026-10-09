"""Runs against droidline-fakephone's demo app. Also a CI smoke test."""
from droidline import connect, NotFoundError

d = connect()
d.home()
d.launch("dev.droidline.demo")
d.dump("screen.json")

if d.exists("text", "Close ad"):
    d.touch("text", "Close ad")
d.input("id", "email", "knife")
if not d.checked("id", "auto_login"):
    d.touchById("auto_login")
d.touchById("login")

state = d.which([("text", "Log in"), ("id", "main_tab")], timeout=5)
assert state == 1, state
assert d.get_text("id", "greeting") == "Welcome, knife"

try:
    d.touch("text", "Does not exist", timeout=0.5)
    raise SystemExit("expected NotFoundError")
except NotFoundError as e:
    print("expected error:", e)

d.screenshot("screen.png", scale=0.5)
result = d.batch([("home",), ("sleep", 500), ("back",)], cuts_network=True, wait=True)
assert all(step["ok"] for step in result["results"])
print("python demo ok")
