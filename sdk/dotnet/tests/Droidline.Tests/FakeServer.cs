using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Text.Json.Nodes;

namespace Droidline.Tests;

/// <summary>In-process fake of the Droidline client API (PROTOCOL.md section 3) for the SDK tests.</summary>
internal sealed class FakeServer : IDisposable
{
    public static readonly byte[] PngBytes = Encoding.Latin1.GetBytes("\x89PNG\r\n\x1a\nfake-image-bytes");

    private readonly TcpListener _listener;
    private readonly object _lock = new();
    private readonly List<JsonObject> _received = new();
    private readonly List<FakeConn> _connections = new();

    public FakeServer()
    {
        _listener = new TcpListener(IPAddress.Loopback, 0);
        _listener.Start();
        Port = ((IPEndPoint)_listener.LocalEndpoint).Port;
        Handler = Default;
        _ = Task.Run(AcceptLoop);
    }

    public int Port { get; }

    public Action<FakeConn, JsonObject> Handler { get; set; }

    public List<JsonObject> Received
    {
        get
        {
            lock (_lock) return _received.ToList();
        }
    }

    public List<FakeConn> Connections
    {
        get
        {
            lock (_lock) return _connections.ToList();
        }
    }

    public static JsonObject Canned(string cmd) => cmd switch
    {
        "exists" => Obj("""{"value": false}"""),
        "get_text" => Obj("""{"value": ""}"""),
        "touch" => Obj("""{"via": "node", "ms": 412}"""),
        "touchById" => Obj("""{"via": "node", "ms": 120}"""),
        "touchByText" => Obj("""{"via": "gesture", "ms": 300}"""),
        "which" => Obj("""{"value": 1}"""),
        "batch" => Obj("""{"results": [{"ok": true}]}"""),
        "chrome.go" => Obj("""{"ms": 900}"""),
        "screenshot" => new JsonObject { ["format"] = "png", ["width"] = 2, ["height"] = 2, ["data"] = Convert.ToBase64String(PngBytes) },
        "dump" => Obj("""
            {"package": "com.kakao.talk", "activity": ".LoginActivity", "width": 1080, "height": 2340,
             "tree": {"text": "로그인", "id": "com.kakao.talk:id/login", "children": []}}
            """),
        "devices" => Obj("""
            {"value": [{"id": "a1b2c3d4", "name": "shelf-01", "online": true}, {"id": "zzzz9999", "name": "shelf-02", "online": true}]}
            """),
        "wait_notification" => Obj("""{"value": {"key": "k1", "package": "com.kakao.talk", "text": "인증번호 1234"}}"""),
        _ => new JsonObject(),
    };

    public static JsonObject Obj(string json) => JsonNode.Parse(json)!.AsObject();

    public void Default(FakeConn conn, JsonObject msg) => conn.Reply(msg, Canned((string)msg["cmd"]!));

    public JsonObject Last(string cmd)
    {
        lock (_lock)
        {
            for (var i = _received.Count - 1; i >= 0; i--)
            {
                if ((string?)_received[i]["cmd"] == cmd) return _received[i];
            }
        }
        throw new Xunit.Sdk.XunitException($"fake server never received {cmd}");
    }

    public JsonObject SentParams(string cmd) => WithoutId(Last(cmd));

    public static JsonObject WithoutId(JsonObject msg)
    {
        var copy = msg.DeepClone().AsObject();
        copy.Remove("id");
        return copy;
    }

    private async Task AcceptLoop()
    {
        while (true)
        {
            TcpClient client;
            try
            {
                client = await _listener.AcceptTcpClientAsync();
            }
            catch (Exception)
            {
                return;
            }
            var conn = new FakeConn(this, client);
            lock (_lock) _connections.Add(conn);
            _ = Task.Run(conn.Serve);
        }
    }

    internal void Record(FakeConn conn, JsonObject msg)
    {
        lock (conn.ReceivedList) conn.ReceivedList.Add(msg);
        lock (_lock) _received.Add(msg);
    }

    public void Dispose()
    {
        _listener.Stop();
        foreach (var conn in Connections) conn.Close();
    }
}

internal sealed class FakeConn
{
    private readonly FakeServer _server;
    private readonly TcpClient _client;
    private readonly NetworkStream _stream;
    private readonly object _sendLock = new();

    public FakeConn(FakeServer server, TcpClient client)
    {
        _server = server;
        _client = client;
        _stream = client.GetStream();
    }

    internal List<JsonObject> ReceivedList { get; } = new();

    public List<JsonObject> Received
    {
        get
        {
            lock (ReceivedList) return ReceivedList.ToList();
        }
    }

    public async Task Serve()
    {
        try
        {
            using var reader = new StreamReader(_stream, new UTF8Encoding(false), false, 4096, leaveOpen: true);
            string? line;
            while ((line = await reader.ReadLineAsync()) is not null)
            {
                var msg = JsonNode.Parse(line)!.AsObject();
                _server.Record(this, msg);
                _server.Handler(this, msg);
            }
        }
        catch (Exception e) when (e is IOException or ObjectDisposedException or SocketException)
        {
        }
    }

    public void Send(JsonObject obj)
    {
        var bytes = Encoding.UTF8.GetBytes(obj.ToJsonString() + "\n");
        lock (_sendLock)
        {
            try
            {
                _stream.Write(bytes, 0, bytes.Length);
            }
            catch (Exception e) when (e is IOException or ObjectDisposedException)
            {
            }
        }
    }

    public void Send(string json) => Send(FakeServer.Obj(json));

    public void Reply(JsonObject msg, JsonObject? fields = null)
    {
        var reply = new JsonObject { ["id"] = msg["id"]!.DeepClone(), ["ok"] = true };
        if (fields is not null)
        {
            foreach (var kv in fields) reply[kv.Key] = kv.Value?.DeepClone();
        }
        Send(reply);
    }

    public void Reply(JsonObject msg, string fields) => Reply(msg, FakeServer.Obj(fields));

    public void Close()
    {
        try
        {
            _client.Client?.Shutdown(SocketShutdown.Both);
        }
        catch (Exception e) when (e is SocketException or ObjectDisposedException)
        {
        }
        _client.Close();
    }
}
