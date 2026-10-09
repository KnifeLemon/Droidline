using System.Collections.Concurrent;
using System.Diagnostics;
using System.Net;
using System.Net.Sockets;
using System.Text.Json;
using System.Text.Json.Nodes;
using Xunit;

// The tests set DROIDLINE_* environment variables, which the whole process shares.
[assembly: CollectionBehavior(DisableTestParallelization = true)]

namespace Droidline.Tests;

public sealed class ClientTests : IDisposable
{
    private static readonly string[] EnvNames = { "DROIDLINE_HOST", "DROIDLINE_PORT", "DROIDLINE_TOKEN", "DROIDLINE_DEVICE" };

    private static readonly JsonObject NodeJson = FakeServer.Obj("""
        {"text": "Wi-Fi", "id": "android:id/title", "desc": "", "class": "android.widget.TextView", "bounds": [40, 330, 400, 380], "checked": false}
        """);

    private readonly FakeServer _server;
    private readonly List<IDisposable> _closers = new();
    private readonly string _temp = Path.Combine(Path.GetTempPath(), "droidline-dotnet-tests", Guid.NewGuid().ToString("N"));

    public ClientTests()
    {
        ClearEnv();
        _server = new FakeServer();
        Directory.CreateDirectory(_temp);
    }

    public void Dispose()
    {
        foreach (var closer in _closers) closer.Dispose();
        ClearEnv();
        _server.Dispose();
        try
        {
            Directory.Delete(_temp, recursive: true);
        }
        catch (IOException)
        {
        }
    }

    private static void ClearEnv()
    {
        foreach (var name in EnvNames) Environment.SetEnvironmentVariable(name, null);
    }

    private async Task<Device> Open(string? device = null, string? token = null)
    {
        var d = await DroidlineClient.ConnectAsync(device, port: _server.Port, token: token);
        _closers.Add(d);
        return d;
    }

    private DroidlineClient NewClient()
    {
        var c = new DroidlineClient(port: _server.Port);
        _closers.Add(c);
        return c;
    }

    private static void AssertJson(string expected, JsonNode? actual) => Assert.Equal(Canon(JsonNode.Parse(expected)), Canon(actual));

    private static void AssertJson(string expected, DroidlineObject actual) => AssertJson(expected, JsonNode.Parse(actual.ToJson()));

    private static string Canon(JsonNode? node) => node switch
    {
        null => "null",
        JsonObject o => "{" + string.Join(",", o.OrderBy(kv => kv.Key, StringComparer.Ordinal).Select(kv => JsonSerializer.Serialize(kv.Key) + ":" + Canon(kv.Value))) + "}",
        JsonArray a => "[" + string.Join(",", a.Select(Canon)) + "]",
        _ => node.ToJsonString(),
    };

    private static async Task WaitUntil(Func<bool> condition, int ms = 3000)
    {
        var watch = Stopwatch.StartNew();
        while (!condition())
        {
            if (watch.ElapsedMilliseconds > ms) throw new Xunit.Sdk.XunitException("condition not met in time");
            await Task.Delay(10);
        }
    }

    private static int FreePort()
    {
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        var port = ((IPEndPoint)listener.LocalEndpoint).Port;
        listener.Stop();
        return port;
    }

    private static JsonObject Error(JsonObject msg, string json)
    {
        var reply = FakeServer.Obj(json);
        reply["id"] = msg["id"]!.DeepClone();
        reply["ok"] = false;
        return reply;
    }

    private static string Cmd(JsonObject msg) => (string)msg["cmd"]!;

    [Fact]
    public async Task SendsOnlyTheParamsTheCallerPassed()
    {
        var d = await Open();
        await d.TouchAsync("id", "com.kakao.talk:id/login");
        AssertJson("""{"cmd": "touch", "by": "id", "value": "com.kakao.talk:id/login"}""", _server.SentParams("touch"));

        await d.TouchAsync("text", "확인", 1, timeout: 5);
        AssertJson("""{"cmd": "touch", "by": "text", "value": "확인", "nth": 1, "timeout": 5}""", _server.SentParams("touch"));

        await d.SwipeAsync(540, 1600, 540, 400, 300);
        AssertJson("""{"cmd": "swipe", "x1": 540, "y1": 1600, "x2": 540, "y2": 400, "ms": 300}""", _server.SentParams("swipe"));
        await d.SwipeAsync("up");
        AssertJson("""{"cmd": "swipe", "x1": "up"}""", _server.SentParams("swipe"));

        await d.SendkeyAsync("enter");
        AssertJson("""{"cmd": "sendkey", "key": "enter"}""", _server.SentParams("sendkey"));
        await d.SendkeyAsync(66);
        AssertJson("""{"cmd": "sendkey", "key": 66}""", _server.SentParams("sendkey"));
        await d.ScrollToAsync("text", "설정", direction: "up", maxSwipes: 5);
        AssertJson("""{"cmd": "scroll_to", "by": "text", "value": "설정", "direction": "up", "max_swipes": 5}""", _server.SentParams("scroll_to"));
    }

    [Fact]
    public async Task DeviceFieldAndReturnKinds()
    {
        var d = await Open("shelf-01");
        Assert.False(await d.ExistsAsync("text", "광고 닫기"));
        var touched = await d.TouchAsync("id", "login");
        Assert.Equal("node", touched.Via);
        Assert.Equal(412, touched.Ms);
        AssertJson("""{"via": "node", "ms": 412}""", touched);
        await d.BackAsync();
        AssertJson("""{"cmd": "back", "device": "shelf-01"}""", _server.SentParams("back"));
    }

    [Fact]
    public async Task ResponsesAreMatchedByIdOutOfOrder()
    {
        var held = new List<(FakeConn Conn, JsonObject Msg)>();
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) is not ("exists" or "get_text"))
            {
                _server.Default(conn, msg);
                return;
            }
            held.Add((conn, msg));
            if (held.Count < 2) return;
            foreach (var (c, m) in Enumerable.Reverse(held)) c.Reply(m, Cmd(m) == "exists" ? """{"value": true}""" : """{"value": "잔액 1,000원"}""");
        };
        var client = NewClient();
        var exists = client.Device("a").ExistsAsync("text", "x");
        var text = client.Device("b").GetTextAsync("id", "balance");
        Assert.True(await exists);
        Assert.Equal("잔액 1,000원", await text);
        Assert.Equal("a", (string)held.Single(h => Cmd(h.Msg) == "exists").Msg["device"]!);
        Assert.Equal("b", (string)held.Single(h => Cmd(h.Msg) == "get_text").Msg["device"]!);
    }

    [Fact]
    public async Task EventsInterleavedWithResponses()
    {
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) != "touch")
            {
                _server.Default(conn, msg);
                return;
            }
            conn.Send("""{"event": "screen", "device": "a1b2c3d4", "package": "com.kakao.talk"}""");
            // A late result for an older request carries an id too; it must not resolve this call.
            conn.Send(new JsonObject { ["event"] = "result", ["id"] = msg["id"]!.DeepClone(), ["ok"] = true, ["results"] = new JsonArray() });
            conn.Reply(msg, """{"via": "node", "ms": 5}""");
            conn.Send("""{"event": "toast", "device": "a1b2c3d4", "text": "저장됨"}""");
        };
        var client = NewClient();
        var events = new ConcurrentQueue<DroidlineEvent>();
        var screens = new ConcurrentQueue<DroidlineEvent>();
        client.On("*", events.Enqueue);
        client.On("screen", screens.Enqueue);

        var touched = await client.Device().TouchAsync("text", "저장");
        AssertJson("""{"via": "node", "ms": 5}""", touched);
        await WaitUntil(() => events.Count == 3);
        Assert.Equal(new[] { "screen", "result", "toast" }, events.Select(e => e.Kind));
        Assert.Same(events.First(), Assert.Single(screens));
        Assert.Equal("a1b2c3d4", events.First().Device);
    }

    [Fact]
    public async Task SlowEventHandlerDoesNotBlockReplies()
    {
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) == "home") conn.Send("""{"event": "toast", "text": "slow"}""");
            _server.Default(conn, msg);
        };
        var client = NewClient();
        using var started = new ManualResetEventSlim();
        client.On("toast", _ =>
        {
            started.Set();
            Thread.Sleep(1000);
        });
        var d = client.Device();
        await d.HomeAsync();
        Assert.True(started.Wait(2000));
        var watch = Stopwatch.StartNew();
        await d.BackAsync();
        Assert.True(watch.ElapsedMilliseconds < 500, $"back took {watch.ElapsedMilliseconds} ms");
    }

    [Fact]
    public async Task ThrowingEventHandlerDoesNotBreakReplies()
    {
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) == "home") conn.Send("""{"event": "toast", "text": "boom"}""");
            _server.Default(conn, msg);
        };
        var original = Console.Error;
        var log = new StringWriter();
        Console.SetError(TextWriter.Synchronized(log));
        try
        {
            var client = NewClient();
            using var sub = client.On("toast", _ => throw new InvalidOperationException("handler bug"));
            var d = client.Device();
            await d.HomeAsync();
            await WaitUntil(() => log.ToString().Contains("handler bug"));
            await d.BackAsync();
        }
        finally
        {
            Console.SetError(original);
        }
    }

    [Fact]
    public async Task ErrorReplyThrowsTheMappedExceptionWithFields()
    {
        _server.Handler = (conn, msg) =>
        {
            switch (Cmd(msg))
            {
                case "input":
                    conn.Send(Error(msg, """
                        {"error": "NOT_FOUND", "msg": "10초 동안 text '아이디' 대상을 찾지 못했습니다. 현재 화면: com.kakao.talk / .LoginActivity",
                         "screen": "com.kakao.talk/.LoginActivity", "retryable": true}
                        """));
                    break;
                case "wait_gone":
                    conn.Send(Error(msg, """{"error": "TIMEOUT", "msg": "wait_gone did not finish"}"""));
                    break;
                case "future":
                    conn.Send(Error(msg, """{"error": "SOMETHING_NEW", "msg": "new", "extra": 1}"""));
                    break;
                default:
                    _server.Default(conn, msg);
                    break;
            }
        };
        var d = await Open();
        var err = await Assert.ThrowsAsync<NotFoundException>(() => d.InputAsync("text", "아이디", "knife"));
        Assert.IsAssignableFrom<DroidlineException>(err);
        Assert.Equal("NOT_FOUND", err.Code);
        Assert.Equal(ErrorCodes.NotFound, err.Code);
        Assert.True(err.Retryable);
        Assert.Equal(422, err.Http);
        AssertJson("""{"screen": "com.kakao.talk/.LoginActivity"}""", err.Fields);
        Assert.Equal("com.kakao.talk/.LoginActivity", err.Fields.Get<string>("screen"));
        Assert.Contains("아이디", err.Message);

        var timeout = await Assert.ThrowsAsync<DroidlineTimeoutException>(() => d.WaitGoneAsync("text", "로딩 중"));
        Assert.True(timeout.Retryable);

        var unknown = await Assert.ThrowsAsync<DroidlineException>(() => d.CallAsync("future"));
        Assert.Equal("SOMETHING_NEW", unknown.Code);
        Assert.False(unknown.Retryable);
        Assert.Null(unknown.Http);
        Assert.Equal(1L, unknown.Fields["extra"]);
    }

    [Fact]
    public async Task ScreenshotSavesToAPathAndReturnsTheBytes()
    {
        var d = await Open();
        var png = Path.Combine(_temp, "a.png");
        Assert.Equal(FakeServer.PngBytes, await d.ScreenshotAsync(png));
        Assert.Equal(FakeServer.PngBytes, File.ReadAllBytes(png));
        AssertJson("""{"cmd": "screenshot", "format": "png"}""", _server.SentParams("screenshot"));

        await d.ScreenshotAsync(Path.Combine(_temp, "b.jpg"), quality: 50);
        AssertJson("""{"cmd": "screenshot", "format": "jpeg", "quality": 50}""", _server.SentParams("screenshot"));

        Assert.Equal(FakeServer.PngBytes, await d.ScreenshotAsync());
        AssertJson("""{"cmd": "screenshot"}""", _server.SentParams("screenshot"));
        await d.ScreenshotAsync(Path.Combine(_temp, "c.jpg"), format: "png", scale: 0.5);
        AssertJson("""{"cmd": "screenshot", "format": "png", "scale": 0.5}""", _server.SentParams("screenshot"));
    }

    [Fact]
    public async Task DumpSavesJsonAndReturnsTheTree()
    {
        var d = await Open();
        var path = Path.Combine(_temp, "screen.json");
        var result = await d.DumpAsync(path);
        Assert.Equal("로그인", result.Tree.Get<string>("text"));
        Assert.Equal("로그인", result.Tree.Text);
        Assert.Equal(1080, result.Width);
        Assert.False(result.Has("id") || result.Has("ok"));
        var text = File.ReadAllText(path);
        Assert.Equal(Canon(JsonNode.Parse(result.ToJson())), Canon(JsonNode.Parse(text)));
        Assert.Contains("로그인", text);
        Assert.Contains("\n  \"tree\": {", text);
        AssertJson("""{"cmd": "dump"}""", _server.SentParams("dump"));

        Assert.Equal("com.kakao.talk", (await d.DumpAsync(allWindows: true)).Package);
        AssertJson("""{"cmd": "dump", "all_windows": true}""", _server.SentParams("dump"));
    }

    [Fact]
    public async Task ShorthandsSendTheAliasAndPascalNamesSendTheSnakeCaseCmd()
    {
        var d = await Open();
        Assert.Equal(120, (await d.TouchByIdAsync("com.kakao.talk:id/login")).Ms);
        AssertJson("""{"cmd": "touchById", "value": "com.kakao.talk:id/login"}""", _server.SentParams("touchById"));
        await d.TouchByTextAsync("확인", nth: 2);
        AssertJson("""{"cmd": "touchByText", "value": "확인", "nth": 2}""", _server.SentParams("touchByText"));

        await d.LongTapAsync(540, 1200, 800);
        AssertJson("""{"cmd": "long_tap", "x": 540, "y": 1200, "ms": 800}""", _server.SentParams("long_tap"));
        await d.WaitGoneAsync("text", "로딩 중", timeout: 3);
        AssertJson("""{"cmd": "wait_gone", "by": "text", "value": "로딩 중", "timeout": 3}""", _server.SentParams("wait_gone"));
    }

    [Fact]
    public async Task ChromeNamespace()
    {
        var d = await Open();
        Assert.Equal(900, (await d.Chrome.GoAsync("naver.com", newTab: true)).Ms);
        AssertJson("""{"cmd": "chrome.go", "url": "naver.com", "new_tab": true}""", _server.SentParams("chrome.go"));
    }

    [Fact]
    public async Task WhichAndBatchSendTuplesAsArrays()
    {
        var d = await Open();
        Assert.Equal(1, await d.WhichAsync(new object[] { ("text", "로그인"), ("id", "x") }, timeout: 10));
        AssertJson("""{"cmd": "which", "candidates": [["text", "로그인"], ["id", "x"]], "timeout": 10}""", _server.SentParams("which"));
        await d.WhichAsync(new object[] { new { text = "로그인" }, new[] { "id", "x" } });
        AssertJson("""{"cmd": "which", "candidates": [{"text": "로그인"}, ["id", "x"]]}""", _server.SentParams("which"));

        await d.BatchAsync(new object[] { ("touch", "text", "Wi-Fi"), ("sleep", 3000), ("touch", "text", "Wi-Fi") });
        AssertJson("""
            {"cmd": "batch", "steps": [["touch", "text", "Wi-Fi"], ["sleep", 3000], ["touch", "text", "Wi-Fi"]]}
            """, _server.SentParams("batch"));
        await d.BatchAsync(new object[] { new object[] { "home" }, new { cmd = "sleep", ms = 500 } }, stopOnError: false);
        AssertJson("""
            {"cmd": "batch", "steps": [["home"], {"cmd": "sleep", "ms": 500}], "stop_on_error": false}
            """, _server.SentParams("batch"));
    }

    [Fact]
    public async Task WaitFlagOnNetworkCuttingBatches()
    {
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) != "batch")
            {
                _server.Default(conn, msg);
                return;
            }
            if (msg["wait"]?.GetValue<bool>() == true)
            {
                conn.Reply(msg, """{"results": [{"ok": true}]}""");
                return;
            }
            conn.Reply(msg, """{"accepted": true}""");
            var late = FakeServer.Obj("""{"event": "result", "device": "a1b2c3d4", "ok": true, "results": [{"ok": true}]}""");
            late["id"] = msg["id"]!.DeepClone();
            conn.Send(late);
        };
        var d = await Open();
        var late = new ConcurrentQueue<DroidlineEvent>();
        d.Client.On("result", late.Enqueue);
        var steps = new object[] { ("touch", "text", "Wi-Fi"), ("sleep", 3000), ("touch", "text", "Wi-Fi") };

        var accepted = await d.BatchAsync(steps, cutsNetwork: true);
        Assert.True(accepted.Accepted);
        Assert.Null(accepted.Results);
        AssertJson("""{"accepted": true}""", accepted);
        Assert.False(_server.Last("batch").ContainsKey("wait"));
        await WaitUntil(() => late.Count == 1);
        AssertJson("""[{"ok": true}]""", JsonNode.Parse(late.First().ToJson())!["results"]);

        var done = await d.BatchAsync(steps, cutsNetwork: true, wait: true);
        Assert.False(done.Accepted);
        Assert.True(done.Results![0].Get<bool>("ok"));
        AssertJson("""{"results": [{"ok": true}]}""", done);
        Assert.True(_server.Last("batch")["wait"]!.GetValue<bool>());
        Assert.True(_server.Last("batch")["cuts_network"]!.GetValue<bool>());
    }

    [Fact]
    public async Task TokenIsSentAsTheFirstLine()
    {
        var d = await Open(token: "dlc_test");
        await d.BackAsync();
        var received = _server.Received;
        AssertJson("""{"cmd": "auth", "token": "dlc_test"}""", FakeServer.WithoutId(received[0]));
        Assert.Equal("back", Cmd(received[1]));
        Assert.DoesNotContain(d.History, e => e.Cmd == "auth");
    }

    [Fact]
    public async Task RejectedTokenThrowsUnauthorized()
    {
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) == "auth") conn.Send(Error(msg, """{"error": "UNAUTHORIZED", "msg": "This connection needs a client token"}"""));
            else _server.Default(conn, msg);
        };
        await Assert.ThrowsAsync<UnauthorizedException>(() => DroidlineClient.ConnectAsync(port: _server.Port, token: "wrong"));
    }

    [Fact]
    public async Task EnvironmentVariables()
    {
        Environment.SetEnvironmentVariable("DROIDLINE_HOST", "127.0.0.1");
        Environment.SetEnvironmentVariable("DROIDLINE_PORT", _server.Port.ToString());
        Environment.SetEnvironmentVariable("DROIDLINE_TOKEN", "dlc_env");
        Environment.SetEnvironmentVariable("DROIDLINE_DEVICE", "shelf-02");
        await using var d = await DroidlineClient.ConnectAsync();
        await d.HomeAsync();
        Assert.Equal("dlc_env", (string)_server.Received[0]["token"]!);
        Assert.Equal("shelf-02", (string)_server.Last("home")["device"]!);
        Assert.Equal("127.0.0.1", d.Client.Host);

        using var explicitPort = new DroidlineClient("localhost", 1234);
        Assert.Equal(("localhost", 1234), (explicitPort.Host, explicitPort.Port));
        Assert.Equal("shelf-03", explicitPort.Device("shelf-03").Id);
        Assert.Equal("shelf-02", explicitPort.Device().Id);
    }

    [Fact]
    public async Task ReconnectsAfterTheServerDropsTheConnection()
    {
        var armed = 1;
        _server.Handler = (conn, msg) =>
        {
            _server.Default(conn, msg);
            if (Cmd(msg) == "back" && Interlocked.Exchange(ref armed, 0) == 1) conn.Close();
        };
        var d = await Open(token: "dlc_test");
        await d.Client.SubscribeAsync(new[] { "device" });
        await d.BackAsync();
        await WaitUntil(() => !d.Client.Connected);

        await d.HomeAsync();
        Assert.Equal(2, _server.Connections.Count);
        var replay = _server.Connections[1].Received.Select(FakeServer.WithoutId).ToList();
        Assert.Equal(3, replay.Count);
        AssertJson("""{"cmd": "auth", "token": "dlc_test"}""", replay[0]);
        AssertJson("""{"cmd": "subscribe", "events": ["device"]}""", replay[1]);
        AssertJson("""{"cmd": "home"}""", replay[2]);
    }

    [Fact]
    public async Task CallInFlightFailsWhenTheConnectionDrops()
    {
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) == "launch") conn.Close();
            else _server.Default(conn, msg);
        };
        var d = await Open();
        var err = await Assert.ThrowsAsync<ConnectionLostException>(() => d.LaunchAsync("com.kakao.talk"));
        Assert.Equal("CONNECTION_LOST", err.Code);
        await d.HomeAsync();
        Assert.Equal("CONNECTION_LOST", d.History.Single(e => e.Cmd == "launch").Error);
    }

    [Fact]
    public async Task OnNotificationFiltersByDevicePackageAndText()
    {
        var d = await Open("shelf-01");
        var got = new ConcurrentQueue<Notification>();
        var everything = new ConcurrentQueue<DroidlineEvent>();
        d.Client.On("notification", everything.Enqueue);
        var stop = await d.OnNotificationAsync(got.Enqueue, package: "com.kakao.talk", textContains: "3시");
        using (await d.OnNotificationAsync(_ => { }))
        {
        }

        var subscribes = _server.Received.Where(m => Cmd(m) == "subscribe").Select(FakeServer.WithoutId).ToList();
        AssertJson("""{"cmd": "subscribe", "events": ["notification"]}""", Assert.Single(subscribes));

        static JsonObject Note(string device, string package, string title, string text) =>
            new() { ["event"] = "notification", ["device"] = device, ["key"] = text, ["package"] = package, ["title"] = title, ["text"] = text };

        var conn = _server.Connections[0];
        conn.Send(Note("a1b2c3d4", "com.kakao.talk", "홍길동", "내일 3시에 봬요"));
        conn.Send(Note("zzzz9999", "com.kakao.talk", "홍길동", "3시 다른 폰"));
        conn.Send(Note("a1b2c3d4", "com.android.chrome", "", "3시 다른 앱"));
        conn.Send(Note("a1b2c3d4", "com.kakao.talk", "홍길동", "안녕"));
        conn.Send(Note("a1b2c3d4", "com.kakao.talk", "3시 회의", "제목에만"));
        await WaitUntil(() => everything.Count == 5);
        Assert.Equal(new[] { "내일 3시에 봬요", "제목에만" }, got.Select(n => n.Get<string>("key")));
        Assert.Equal("홍길동", got.First().Title);

        stop.Dispose();
        conn.Send(Note("a1b2c3d4", "com.kakao.talk", "", "3시 again"));
        await WaitUntil(() => everything.Count == 6);
        Assert.Equal(2, got.Count);
    }

    [Fact]
    public async Task ServerLevelAndDeviceLevelWaitNotification()
    {
        var client = NewClient();
        var n = await client.WaitNotificationAsync("textContains", "인증번호", 60);
        Assert.Equal("인증번호 1234", n.Get<string>("text"));
        Assert.Equal("인증번호 1234", n.Text);
        AssertJson("""{"cmd": "wait_notification", "by": "textContains", "value": "인증번호", "timeout": 60}""", _server.SentParams("wait_notification"));
        await client.Device("shelf-01").WaitNotificationAsync("textContains", "인증번호");
        Assert.Equal("shelf-01", (string)_server.Last("wait_notification")["device"]!);

        var devices = await client.DevicesAsync();
        Assert.Equal(new object?[] { "shelf-01", "shelf-02" }, devices.Select(x => x.Name));
        Assert.True(devices[0].Get<bool>("online"));
    }

    [Fact]
    public async Task CallEscapeHatch()
    {
        var d = await Open("shelf-01");
        Assert.Empty(await d.CallAsync("future_cmd", new { foo = 1 }));
        AssertJson("""{"cmd": "future_cmd", "device": "shelf-01", "foo": 1}""", _server.SentParams("future_cmd"));
        await d.Client.CallAsync("server_cmd", new Dictionary<string, object?> { ["bar"] = new[] { 1, 2 } });
        AssertJson("""{"cmd": "server_cmd", "bar": [1, 2]}""", _server.SentParams("server_cmd"));
    }

    [Fact]
    public async Task DisposingAConnectedDeviceClosesTheConnection()
    {
        var d = await DroidlineClient.ConnectAsync(port: _server.Port);
        await d.BackAsync();
        Assert.True(d.Client.Connected);
        await d.DisposeAsync();
        Assert.False(d.Client.Connected);

        var client = NewClient();
        var handle = client.Device("shelf-01");
        await handle.BackAsync();
        handle.Dispose();
        Assert.True(client.Connected);
    }

    [Fact]
    public async Task ServerNotRunningNamesDroidlineServe()
    {
        var err = await Assert.ThrowsAsync<ServerNotRunningException>(() => DroidlineClient.ConnectAsync(port: FreePort()));
        Assert.Contains("droidline serve", err.Message);
        Assert.Equal("SERVER_NOT_RUNNING", err.Code);
    }

    [Fact]
    public async Task QuerySelectorsKeepTheirKeysAndInputTakesTextAfterAQuery()
    {
        var d = await Open();
        await d.TouchAsync(new { text = "확인", clickable = true }, timeout: 5);
        AssertJson("""{"cmd": "touch", "by": {"text": "확인", "clickable": true}, "timeout": 5}""", _server.SentParams("touch"));
        await d.InputAsync(new { editable = true }, "knife");
        AssertJson("""{"cmd": "input", "by": {"editable": true}, "text": "knife"}""", _server.SentParams("input"));
        await d.InputAsync("id", "email", "knife");
        AssertJson("""{"cmd": "input", "by": "id", "value": "email", "text": "knife"}""", _server.SentParams("input"));

        await d.TouchAsync(new { textContains = "Wi", long_clickable = true, left_of = new { id = "switch" }, @class = "android.widget.TextView" });
        AssertJson("""
            {"cmd": "touch", "by": {"textContains": "Wi", "long_clickable": true, "left_of": {"id": "switch"}, "class": "android.widget.TextView"}}
            """, _server.SentParams("touch"));

        var query = new Query { Class = "android.widget.Switch", Row = new Query { Text = "Wi-Fi" }, Visible = true, LongClickable = false };
        query.LeftOf = new Query { DescContains = "x" };
        await d.TouchAsync(query);
        AssertJson("""
            {"cmd": "touch", "by": {"class": "android.widget.Switch", "row": {"text": "Wi-Fi"}, "visible": true, "long_clickable": false, "left_of": {"descContains": "x"}}}
            """, _server.SentParams("touch"));
        Assert.Equal("Wi-Fi", query.Row!.Text);
        query.Visible = null;
        Assert.False(query.ContainsKey("visible"));

        await d.ExistsAsync(new Dictionary<string, object?> { ["descMatches"] = "^OK$", ["bounds"] = new[] { 0, 0, 10, 10 } });
        AssertJson("""{"cmd": "exists", "by": {"descMatches": "^OK$", "bounds": [0, 0, 10, 10]}}""", _server.SentParams("exists"));
    }

    [Fact]
    public async Task FindReturnsElementsThatActOnThemselves()
    {
        _server.Handler = (conn, msg) =>
        {
            switch (Cmd(msg))
            {
                case "find":
                    conn.Reply(msg, new JsonObject { ["value"] = NodeJson.DeepClone() });
                    break;
                case "find_all":
                    var second = NodeJson.DeepClone().AsObject();
                    second["text"] = "Bluetooth";
                    second["bounds"] = new JsonArray(40, 530, 400, 580);
                    conn.Reply(msg, new JsonObject { ["value"] = new JsonArray(NodeJson.DeepClone(), second) });
                    break;
                default:
                    _server.Default(conn, msg);
                    break;
            }
        };
        var d = await Open();
        var el = await d.FindAsync("text", "Wi-Fi");
        Assert.Equal(("Wi-Fi", "android:id/title", "android.widget.TextView"), (el.Text, el.Id, el.ClassName));
        Assert.Equal((40, 330, 400, 380), el.Bounds);
        Assert.Equal((220, 355), el.Center);
        Assert.Equal(false, el["checked"]);
        Assert.Equal("Element(android.widget.TextView \"Wi-Fi\" [40, 330, 400, 380])", el.ToString());

        await el.ClickAsync();
        AssertJson("""
            {"cmd": "touch", "by": {"bounds": [40, 330, 400, 380], "class": "android.widget.TextView"}, "timeout": 0}
            """, _server.SentParams("touch"));
        await el.InputAsync("knife", append: true);
        AssertJson("""
            {"cmd": "input", "by": {"bounds": [40, 330, 400, 380], "class": "android.widget.TextView"}, "text": "knife", "append": true, "timeout": 0}
            """, _server.SentParams("input"));
        await el.FindAsync("id", "summary");
        AssertJson("""
            {"id": "summary", "inside": {"bounds": [40, 330, 400, 380], "class": "android.widget.TextView"}}
            """, _server.SentParams("find")["by"]);
        await el.FindAsync(new { textContains = "On" }, timeout: 2);
        AssertJson("""
            {"cmd": "find", "by": {"textContains": "On", "inside": {"bounds": [40, 330, 400, 380], "class": "android.widget.TextView"}}, "timeout": 2}
            """, _server.SentParams("find"));
        await Assert.ThrowsAsync<ArgumentException>(() => el.FindAsync(new { inside = new { id = "x" } }));

        var all = await d.FindAllAsync("id", "title");
        Assert.Equal(new[] { "Wi-Fi", "Bluetooth" }, all.Select(e => e.Text));
        Assert.Equal(el, all[0]);
        Assert.NotEqual(el, all[1]);
    }

    [Fact]
    public async Task LeaseCarriesTheLeaseAndReleases()
    {
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) == "lease") conn.Reply(msg, """{"lease": "l-1", "device": "a1b2c3d4", "name": "shelf-01", "ttl": 300}""");
            else _server.Default(conn, msg);
        };
        await using (var d = await DroidlineClient.LeaseAsync(wait: 5, port: _server.Port))
        {
            await d.HomeAsync();
            AssertJson("""{"cmd": "home", "device": "a1b2c3d4", "lease": "l-1"}""", _server.SentParams("home"));
            Assert.Equal(("shelf-01", 300.0, "l-1"), (d.Name, d.Ttl, d.LeaseId));
        }
        AssertJson("""{"cmd": "release", "lease": "l-1"}""", _server.SentParams("release"));
        AssertJson("""{"cmd": "lease", "wait": 5}""", _server.SentParams("lease"));

        var client = NewClient();
        var leased = await client.LeaseAsync(device: "shelf-01", minSdk: 33);
        AssertJson("""{"cmd": "lease", "device": "shelf-01", "min_sdk": 33}""", _server.SentParams("lease"));
        await leased.ReleaseAsync();
        await leased.ReleaseAsync();
        Assert.Equal(2, _server.Received.Count(m => Cmd(m) == "release"));
        Assert.Null(leased.LeaseId);
        leased.Dispose();
        Assert.True(client.Connected);

        await client.ReleaseAsync(device: "shelf-01");
        AssertJson("""{"cmd": "release", "device": "shelf-01"}""", _server.SentParams("release"));
    }

    [Fact]
    public async Task HistoryAndImageParams()
    {
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) == "exists") conn.Send(Error(msg, """{"error": "NOT_FOUND", "msg": "not there"}"""));
            else _server.Default(conn, msg);
        };
        var d = await Open("shelf-01");
        await d.HomeAsync();
        await Assert.ThrowsAsync<NotFoundException>(() => d.ExistsAsync("text", "x"));
        var entries = d.History;
        Assert.Equal("home", entries[^2].Cmd);
        Assert.True(entries[^2].Ok);
        Assert.False(entries[^1].Ok);
        Assert.Equal("NOT_FOUND", entries[^1].Error);
        var log = Testing.FormatHistory(entries);
        Assert.Matches(@"^\d\d:\d\d:\d\d home \{\} -> ok \(\d+ ms\)\n", log);
        Assert.Contains("""exists {"by": "text", "value": "x"} -> NOT_FOUND""", log);

        await d.InputAsync("id", "memo", new string('a', 300));
        Assert.Equal("<300 characters>", d.History[^1].Params["text"]);

        var pic = Path.Combine(_temp, "button.png");
        File.WriteAllBytes(pic, FakeServer.PngBytes);
        await d.FindImageAsync(pic, threshold: 0.8);
        var sent = _server.SentParams("find_image");
        Assert.Equal(Convert.ToBase64String(FakeServer.PngBytes), (string)sent["image"]!);
        Assert.Equal(0.8, sent["threshold"]!.GetValue<double>());
        await d.TapImageAsync(FakeServer.PngBytes);
        Assert.Equal((string)sent["image"]!, (string)_server.SentParams("tap_image")["image"]!);

        var other = d.Client.Device("shelf-02");
        await other.BackAsync();
        Assert.DoesNotContain(d.History, e => e.Device == "shelf-02");
        for (var i = 0; i < 205; i++) await other.BackAsync();
        Assert.Equal(200, d.Client.History.Count);
    }

    [Fact]
    public async Task CancellationStopsWaitingAndALateReplyIsIgnored()
    {
        JsonObject? held = null;
        FakeConn? heldConn = null;
        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) == "launch")
            {
                heldConn = conn;
                held = msg;
            }
            else _server.Default(conn, msg);
        };
        var d = await Open();
        using var cts = new CancellationTokenSource(200);
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => d.LaunchAsync("com.kakao.talk", cancellationToken: cts.Token));
        await WaitUntil(() => held is not null);
        heldConn!.Reply(held!, """{"ms": 1}""");
        Assert.Equal(412, (await d.TouchAsync("text", "x")).Ms);
        Assert.True(d.Client.Connected);
    }

    [Fact]
    public async Task SaveFailureWritesTheLogScreenshotAndTree()
    {
        var d = await Open("shelf-01");
        await d.HomeAsync();
        var folder = Path.Combine(_temp, "artifacts", "login");
        var saved = await Testing.SaveFailureAsync(d, folder);
        Assert.Equal(new[] { "commands.txt", "screen.png", "screen.json" }, saved.Select(Path.GetFileName));
        Assert.Contains("home {} -> ok", File.ReadAllText(Path.Combine(folder, "commands.txt")));
        Assert.Equal(FakeServer.PngBytes, File.ReadAllBytes(Path.Combine(folder, "screen.png")));
        Assert.Contains("로그인", File.ReadAllText(Path.Combine(folder, "screen.json")));
        AssertJson("""{"cmd": "screenshot", "device": "shelf-01", "format": "png"}""", _server.SentParams("screenshot"));

        _server.Handler = (conn, msg) =>
        {
            if (Cmd(msg) is "screenshot" or "dump") conn.Send(Error(msg, """{"error": "DEVICE_OFFLINE", "msg": "offline"}"""));
            else _server.Default(conn, msg);
        };
        var offline = await Testing.SaveFailureAsync(d, Path.Combine(_temp, "offline"));
        Assert.Equal("commands.txt", Path.GetFileName(Assert.Single(offline)));
    }

    [Fact]
    public void GeneratedConstantsMatchTheSpec()
    {
        Assert.Equal(1, DroidlineClient.ProtocolVersion);
        Assert.Equal("DEVICE_LEASED", ErrorCodes.DeviceLeased);
        var e = new NotFoundException("gone");
        Assert.True(e.Retryable);
        Assert.Equal(("NOT_FOUND", 422), (e.Code, e.Http));
        Assert.False(new BadArgsException("bad").Retryable);
    }
}
