// Runs against droidline-fakephone's demo app, like demo.py and demo.mjs.
using Droidline;

await using var d = await DroidlineClient.ConnectAsync();
await d.HomeAsync();
await d.LaunchAsync("dev.droidline.demo");
await d.DumpAsync("screen.json");

if (await d.ExistsAsync("text", "Close ad"))
    await d.TouchAsync("text", "Close ad");
await d.InputAsync("id", "email", "dotnet");
if (!await d.CheckedAsync("id", "auto_login"))
    await d.TouchByIdAsync("auto_login");
await d.TouchByIdAsync("login");

var state = await d.WhichAsync(new object[] { ("text", "Log in"), ("id", "main_tab") }, timeout: 5);
Check(state == 1, $"which returned {state}");
var greeting = await d.GetTextAsync("id", "greeting");
Check(greeting == "Welcome, dotnet", $"greeting was {greeting}");

try
{
    await d.TouchAsync("text", "Does not exist", timeout: 0.5);
    Check(false, "expected NotFoundException");
}
catch (NotFoundException e)
{
    Console.WriteLine($"expected error: {e.Message}");
}

var png = await d.ScreenshotAsync("screen.png", scale: 0.5);
Check(png.Length > 100, "screenshot is empty");
var result = await d.BatchAsync(new object[] { new object[] { "home" }, ("sleep", 500), new object[] { "back" } }, cutsNetwork: true, wait: true);
Check(result.Results!.All(step => step.Get<bool>("ok")), result.ToJson());
Console.WriteLine("dotnet demo ok");

static void Check(bool ok, string message)
{
    if (!ok) throw new InvalidOperationException(message);
}
