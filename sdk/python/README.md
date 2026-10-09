# droidline

[![Droidline: Android automation, one line at a time.](https://raw.githubusercontent.com/KnifeLemon/Droidline/main/docs/media/banner-en.jpg)](https://droidline.dev)

Python SDK for [Droidline](https://droidline.dev), Android automation from code. Test your apps on real phones, script the tasks you repeat and run many phones at once, without ADB, a USB cable or root.

The SDK talks to the Droidline server on your PC (`droidline serve`, port 8780). The server and the phone do the waiting, retries and fallbacks; this package sends commands and turns errors into exceptions. Python 3.9+, no dependencies.

## Install

```sh
pip install droidline
```

## Example

```python
from droidline import connect

d = connect()
d.launch("com.kakao.talk")
d.dump("screen.json")
d.touchById("com.kakao.talk:id/login")
d.input("id", "com.kakao.talk:id/email", "knife")
d.sendkey("enter")
if d.exists("text", "광고 닫기"):
    d.touch("text", "광고 닫기")
d.intent("android.settings.WIFI_SETTINGS")    # open a Settings page by its intent action
d.wait_idle()
```

`connect()` uses the only online phone. With several, pass an ID or name: `connect("shelf-01")`, or use `Droidline().device("shelf-01")` for each. `DROIDLINE_HOST`, `DROIDLINE_PORT`, `DROIDLINE_TOKEN` and `DROIDLINE_DEVICE` set the defaults.

A failed command raises a subclass of `DroidlineError`, such as `NotFoundError`, with `code`, `retryable` and the server's extra fields in `data`.

Docs: https://droidline.dev/docs
