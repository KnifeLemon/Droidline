# Droidline

C#/.NET SDK for [Droidline](https://droidline.dev), Android automation from code. Test your apps on real phones, script the tasks you repeat and run many phones at once, without ADB, a USB cable or root.

The SDK talks to the Droidline server on your PC (`droidline serve`, port 8780). The server and the phone do the waiting, retries and fallbacks; this package sends commands and turns errors into exceptions. It targets .NET Standard 2.0 and .NET 8, so it also runs on .NET Framework 4.6.2 and later.

## Install

```sh
dotnet add package Droidline
```

## Example

```csharp
using Droidline;

await using var d = await DroidlineClient.ConnectAsync();
await d.LaunchAsync("com.kakao.talk");
await d.DumpAsync("screen.json");
await d.TouchByIdAsync("com.kakao.talk:id/login");
await d.InputAsync("id", "com.kakao.talk:id/email", "knife");
await d.SendkeyAsync("enter");
if (await d.ExistsAsync("text", "광고 닫기"))
    await d.TouchAsync("text", "광고 닫기");
await d.IntentAsync("android.settings.WIFI_SETTINGS");    // open a Settings page by its intent action
await d.WaitIdleAsync();
```

Method names are the protocol's commands in PascalCase with `Async`: `long_tap` is `LongTapAsync`, `chrome.go` is `d.Chrome.GoAsync`. Optional parameters are optional C# parameters, so pass them by name: `d.WaitAsync("text", "완료", timeout: 30)`. Every method takes a `CancellationToken` last.

`ConnectAsync()` uses the only online phone. With several, pass an ID or name: `ConnectAsync("shelf-01")`, or use `new DroidlineClient().Device("shelf-01")` for each. `DROIDLINE_HOST`, `DROIDLINE_PORT`, `DROIDLINE_TOKEN` and `DROIDLINE_DEVICE` set the defaults.

## Query selectors

Pass an object instead of a field and value. A `Query`, a dictionary or an anonymous object works; anonymous object property names are sent as they are, so write the keys the way the protocol spells them:

```csharp
await d.TouchAsync(new { @class = "android.widget.Switch", row = new { text = "Wi-Fi" } });
await d.TouchAsync(new Query { Class = "android.widget.Switch", Row = new Query { Text = "Wi-Fi" } });
var title = await d.FindAsync(new { textContains = "Wi", long_clickable = true });
await title.ClickAsync();
```

`FindAsync` and `FindAllAsync` return elements you can click, type into and search inside.

## Results and errors

Commands that answer with fields return a result object with typed properties (`TouchResult.Via`, `DumpResult.Tree`). Every result is also a read-only dictionary, and `Get<T>("field")` reads any field, including ones newer than this SDK.

A failed command throws a subclass of `DroidlineException`, such as `NotFoundException`, with `Code`, `Retryable` and the server's extra fields in `Fields`. A dropped connection throws `ConnectionLostException`; the next call reconnects.

```csharp
try
{
    await d.TouchAsync("text", "Log in", timeout: 5);
}
catch (NotFoundException e)
{
    Console.WriteLine(e.Fields.Get<string>("screen"));
}
```

## Events, leases and tests

`client.On("notification", e => ...)` and `d.OnNotificationAsync(n => ...)` deliver events on a background thread and return an `IDisposable` that stops them. `DroidlineClient.LeaseAsync()` borrows a free phone so no other script can use it until you dispose it. `Testing.SaveFailureAsync(d, folder)` saves a screenshot, the screen tree and the command log after a failed test.

Docs: https://droidline.dev/docs
