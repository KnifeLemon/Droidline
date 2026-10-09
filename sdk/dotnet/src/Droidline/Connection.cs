using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;

namespace Droidline;

/// <summary>
/// Pipelines NDJSON requests over one socket and matches replies by id (spec/PROTOCOL.md section 3). A reader task
/// per socket routes replies to waiting callers and hands events to a separate thread, so a slow event handler never
/// delays a reply.
/// </summary>
internal sealed class Connection
{
    private const int HistorySize = 200;
    private const string LostEarly = "The connection to the Droidline server dropped. Retry the call.";
    private const string LostInFlight =
        "The connection to the Droidline server dropped before the reply arrived. The command may or may not have run.";

    private static readonly Encoding Utf8 = new UTF8Encoding(false);
    private static readonly HashSet<string> Unlogged = new(StringComparer.Ordinal) { "id", "cmd", "device", "lease", "token" };

    private readonly object _lock = new();
    private readonly SemaphoreSlim _connectLock = new(1, 1);
    private readonly List<string> _subscriptionKeys = new();
    private readonly List<Params> _subscriptions = new();
    private readonly Dictionary<string, List<Action<DroidlineEvent>>> _listeners = new(StringComparer.Ordinal);
    private readonly Queue<HistoryEntry> _history = new();
    private long _nextId;
    private Link? _link;
    private EventPump? _pump;

    public Connection(string host, int port, string? token)
    {
        Host = host;
        Port = port;
        Token = token;
    }

    public string Host { get; }

    public int Port { get; }

    public string? Token { get; private set; }

    public bool Connected
    {
        get
        {
            var link = _link;
            return link is not null && link.Alive;
        }
    }

    public IReadOnlyList<HistoryEntry> History
    {
        get
        {
            lock (_lock) return _history.ToList();
        }
    }

    public async Task<JsonElement> RequestAsync(Params msg, CancellationToken cancellationToken)
    {
        var time = DateTimeOffset.Now;
        var watch = Stopwatch.StartNew();
        JsonElement reply;
        try
        {
            var link = await EnsureAsync(cancellationToken).ConfigureAwait(false);
            reply = await ExchangeAsync(link, msg, cancellationToken).ConfigureAwait(false);
        }
        catch (DroidlineException e)
        {
            Note(msg, time, watch, e.Code);
            throw;
        }
        Note(msg, time, watch, null);
        var cmd = msg.TryGetValue("cmd", out var c) ? c as string : null;
        if (cmd == "subscribe")
        {
            var key = Json.Serialize(msg);
            lock (_lock)
            {
                if (!_subscriptionKeys.Contains(key))
                {
                    _subscriptionKeys.Add(key);
                    _subscriptions.Add(Copy(msg));
                }
            }
        }
        else if (cmd == "auth")
        {
            Token = msg.TryGetValue("token", out var token) ? token as string : null;
        }
        return reply;
    }

    public async Task SubscribeOnceAsync(Params msg, CancellationToken cancellationToken)
    {
        var key = Json.Serialize(msg);
        bool known;
        lock (_lock) known = _subscriptionKeys.Contains(key);
        if (!known) await RequestAsync(msg, cancellationToken).ConfigureAwait(false);
    }

    public async Task<Link> EnsureAsync(CancellationToken cancellationToken)
    {
        var link = _link;
        if (link is not null && link.Alive) return link;
        await _connectLock.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            link = _link;
            if (link is not null && link.Alive) return link;
            link = await OpenAsync(cancellationToken).ConfigureAwait(false);
            try
            {
                // Auth must be the first line, and subscriptions belong to the socket, so a reconnect replays both
                // before any other request goes out.
                if (!string.IsNullOrEmpty(Token))
                {
                    await ExchangeAsync(link, new Params { ["cmd"] = "auth", ["token"] = Token }, cancellationToken).ConfigureAwait(false);
                }
                List<Params> subscriptions;
                lock (_lock) subscriptions = _subscriptions.ToList();
                foreach (var sub in subscriptions) await ExchangeAsync(link, sub, cancellationToken).ConfigureAwait(false);
            }
            catch
            {
                Drop(link);
                throw;
            }
            lock (_lock) _link = link;
            return link;
        }
        finally
        {
            _connectLock.Release();
        }
    }

    private async Task<Link> OpenAsync(CancellationToken cancellationToken)
    {
        var client = new TcpClient { NoDelay = true };
        try
        {
            var connect = client.ConnectAsync(Host, Port);
            var done = await Task.WhenAny(connect, Task.Delay(TimeSpan.FromSeconds(5), cancellationToken)).ConfigureAwait(false);
            if (done != connect)
            {
                _ = connect.ContinueWith(t => _ = t.Exception, TaskScheduler.Default);
                cancellationToken.ThrowIfCancellationRequested();
                throw NotRunning("timed out");
            }
            await connect.ConfigureAwait(false);
        }
        catch (SocketException e)
        {
            client.Dispose();
            throw NotRunning(e.SocketErrorCode.ToString());
        }
        catch
        {
            client.Dispose();
            throw;
        }
        var link = new Link(client);
        _ = Task.Run(() => ReadLoopAsync(link));
        return link;
    }

    private ServerNotRunningException NotRunning(string reason) =>
        new($"Cannot reach the Droidline server at {Host}:{Port} ({reason}). Start it with `droidline serve`.");

    private async Task<JsonElement> ExchangeAsync(Link link, Params msg, CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var waiter = new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);
        long id;
        lock (_lock)
        {
            if (!link.Alive) throw new ConnectionLostException(LostEarly);
            id = ++_nextId;
            link.Pending[id] = waiter;
        }
        var line = new Params { ["id"] = id };
        foreach (var kv in msg) line[kv.Key] = kv.Value;
        var bytes = Utf8.GetBytes(Json.Serialize(line) + "\n");
        try
        {
            // A write cut short by cancellation would leave half a line on the socket, so the write itself is not cancelled.
            await link.SendLock.WaitAsync().ConfigureAwait(false);
            try
            {
                await link.Stream.WriteAsync(bytes, 0, bytes.Length).ConfigureAwait(false);
            }
            finally
            {
                link.SendLock.Release();
            }
        }
        catch (Exception e) when (e is IOException or ObjectDisposedException or SocketException or InvalidOperationException)
        {
            Drop(link);
        }

        JsonElement reply;
        using (cancellationToken.Register(() => Cancel(link, id, waiter, cancellationToken)))
        {
            reply = await waiter.Task.ConfigureAwait(false);
        }
        if (reply.TryGetProperty("ok", out var ok) && ok.ValueKind == JsonValueKind.False) throw ErrorFrom(reply);
        return reply;
    }

    private void Cancel(Link link, long id, TaskCompletionSource<JsonElement> waiter, CancellationToken cancellationToken)
    {
        bool removed;
        lock (_lock) removed = link.Pending.Remove(id);
        if (removed) waiter.TrySetCanceled(cancellationToken);
    }

    internal static DroidlineException ErrorFrom(JsonElement reply)
    {
        static string? Text(JsonElement obj, string name) =>
            obj.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.String && v.GetString() is { Length: > 0 } s ? s : null;

        var code = Text(reply, "error") ?? "INTERNAL";
        bool? retryable = null;
        if (reply.TryGetProperty("retryable", out var r) && r.ValueKind is JsonValueKind.True or JsonValueKind.False) retryable = r.GetBoolean();
        var fields = new DroidlineObject();
        fields.Load(reply, new[] { "id", "ok", "error", "msg", "retryable" });
        return ErrorFactory.Create(code, Text(reply, "msg") ?? code, retryable, fields);
    }

    private async Task ReadLoopAsync(Link link)
    {
        try
        {
            using var reader = new StreamReader(link.Stream, Utf8, false, 16384, leaveOpen: true);
            string? line;
            while ((line = await reader.ReadLineAsync().ConfigureAwait(false)) is not null) Dispatch(link, line);
        }
        catch (Exception)
        {
            // A reset or a closed socket ends the loop; Drop below fails whatever is still waiting.
        }
        finally
        {
            Drop(link);
        }
    }

    private void Dispatch(Link link, string line)
    {
        JsonElement msg;
        try
        {
            using var doc = JsonDocument.Parse(line);
            msg = doc.RootElement.Clone();
        }
        catch (JsonException)
        {
            return;
        }
        if (msg.ValueKind != JsonValueKind.Object) return;
        // Late results carry the request id too, so the event check comes first.
        if (msg.TryGetProperty("event", out _))
        {
            Emit(msg);
            return;
        }
        if (!msg.TryGetProperty("id", out var idElement) || idElement.ValueKind != JsonValueKind.Number || !idElement.TryGetInt64(out var id)) return;
        TaskCompletionSource<JsonElement>? waiter;
        lock (_lock)
        {
            if (link.Pending.TryGetValue(id, out waiter)) link.Pending.Remove(id);
        }
        waiter?.TrySetResult(msg);
    }

    private void Drop(Link link)
    {
        bool wasAlive;
        List<TaskCompletionSource<JsonElement>> pending;
        lock (_lock)
        {
            wasAlive = link.Alive;
            link.Alive = false;
            pending = link.Pending.Values.ToList();
            link.Pending.Clear();
            if (_link == link) _link = null;
        }
        if (wasAlive)
        {
            try
            {
                link.Client.Client.Shutdown(SocketShutdown.Both);
            }
            catch (Exception e) when (e is SocketException or ObjectDisposedException)
            {
            }
            link.Client.Dispose();
        }
        foreach (var waiter in pending) waiter.TrySetException(new ConnectionLostException(LostInFlight));
    }

    public void AddListener(string kind, Action<DroidlineEvent> handler)
    {
        lock (_lock)
        {
            if (!_listeners.TryGetValue(kind, out var list)) _listeners[kind] = list = new List<Action<DroidlineEvent>>();
            list.Add(handler);
        }
    }

    public void RemoveListener(string kind, Action<DroidlineEvent> handler)
    {
        lock (_lock)
        {
            if (_listeners.TryGetValue(kind, out var list)) list.Remove(handler);
        }
    }

    private void Emit(JsonElement ev)
    {
        EventPump pump;
        lock (_lock) pump = _pump ??= new EventPump(Deliver);
        pump.Post(ev);
    }

    private void Deliver(JsonElement element)
    {
        var ev = new DroidlineEvent();
        ev.Load(element);
        var handlers = new List<Action<DroidlineEvent>>();
        lock (_lock)
        {
            if (_listeners.TryGetValue(ev.Kind, out var forKind)) handlers.AddRange(forKind);
            if (_listeners.TryGetValue("*", out var forAll)) handlers.AddRange(forAll);
        }
        foreach (var handler in handlers)
        {
            try
            {
                handler(ev);
            }
            catch (Exception e)
            {
                Console.Error.WriteLine(e);
            }
        }
    }

    private void Note(Params msg, DateTimeOffset time, Stopwatch watch, string? error)
    {
        var cmd = msg.TryGetValue("cmd", out var c) ? c as string ?? "" : "";
        if (cmd == "auth") return;
        var parameters = new Dictionary<string, object?>(StringComparer.Ordinal);
        foreach (var kv in msg)
        {
            if (Unlogged.Contains(kv.Key)) continue;
            var value = Json.Normalize(kv.Value);
            if (value is string s && s.Length > 200) value = $"<{s.Length} characters>";
            parameters[kv.Key] = value;
        }
        var device = msg.TryGetValue("device", out var d) ? d as string : null;
        var entry = new HistoryEntry(time, device, cmd, parameters, error, watch.ElapsedMilliseconds);
        lock (_lock)
        {
            _history.Enqueue(entry);
            while (_history.Count > HistorySize) _history.Dequeue();
        }
    }

    private static Params Copy(Params msg)
    {
        var copy = new Params();
        foreach (var kv in msg) copy[kv.Key] = kv.Value;
        return copy;
    }

    public void Close()
    {
        EventPump? pump;
        Link? link;
        lock (_lock)
        {
            pump = _pump;
            _pump = null;
            link = _link;
        }
        pump?.Stop();
        if (link is not null) Drop(link);
    }

    internal sealed class Link
    {
        public Link(TcpClient client)
        {
            Client = client;
            Stream = client.GetStream();
        }

        public TcpClient Client { get; }

        public NetworkStream Stream { get; }

        public SemaphoreSlim SendLock { get; } = new(1, 1);

        public Dictionary<long, TaskCompletionSource<JsonElement>> Pending { get; } = new();

        public volatile bool Alive = true;
    }

    private sealed class EventPump
    {
        private readonly BlockingCollection<JsonElement> _queue = new();

        public EventPump(Action<JsonElement> deliver)
        {
            var thread = new Thread(() =>
            {
                foreach (var ev in _queue.GetConsumingEnumerable()) deliver(ev);
            })
            {
                IsBackground = true,
                Name = "droidline-events",
            };
            thread.Start();
        }

        public void Post(JsonElement ev)
        {
            try
            {
                _queue.Add(ev);
            }
            catch (InvalidOperationException)
            {
                // Closed: events after Close are dropped.
            }
        }

        public void Stop() => _queue.CompleteAdding();
    }
}
