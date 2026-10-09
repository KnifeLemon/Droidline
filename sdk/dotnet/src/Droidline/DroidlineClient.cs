using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;

namespace Droidline;

/// <summary>
/// Client for the local Droidline server. Commands for one phone go through <see cref="Device(string)"/>; the
/// commands the server answers itself, such as <c>DevicesAsync</c>, are methods of this class.
/// </summary>
/// <remarks>
/// Arguments fall back to DROIDLINE_HOST, DROIDLINE_PORT and DROIDLINE_TOKEN, then to 127.0.0.1:8780 without a token.
/// The socket opens on the first call and reopens after a drop. One client can be shared by many tasks; requests are
/// pipelined and replies matched by id.
/// </remarks>
public sealed partial class DroidlineClient : IDisposable, IAsyncDisposable
{
    /// <summary>The host used when neither an argument nor DROIDLINE_HOST gives one.</summary>
    public const string DefaultHost = "127.0.0.1";

    /// <summary>The client port used when neither an argument nor DROIDLINE_PORT gives one.</summary>
    public const int DefaultPort = 8780;

    /// <summary>Creates a client. Nothing connects until the first call.</summary>
    /// <param name="host">Server host. Default: DROIDLINE_HOST, else 127.0.0.1.</param>
    /// <param name="port">Client API port. Default: DROIDLINE_PORT, else 8780.</param>
    /// <param name="token">Client token from <c>droidline token create</c>. Default: DROIDLINE_TOKEN, else none.</param>
    public DroidlineClient(string? host = null, int? port = null, string? token = null)
    {
        Host = !string.IsNullOrEmpty(host) ? host! : Env("DROIDLINE_HOST") ?? DefaultHost;
        Port = port ?? EnvPort() ?? DefaultPort;
        Connection = new Connection(Host, Port, !string.IsNullOrEmpty(token) ? token : Env("DROIDLINE_TOKEN"));
    }

    internal Connection Connection { get; }

    /// <summary>The server host.</summary>
    public string Host { get; }

    /// <summary>The server's client API port.</summary>
    public int Port { get; }

    /// <summary>True while the socket to the server is open.</summary>
    public bool Connected => Connection.Connected;

    /// <summary>The last 200 requests on this connection, oldest first.</summary>
    public IReadOnlyList<HistoryEntry> History => Connection.History;

    /// <summary>A handle for one phone. Without an argument it uses DROIDLINE_DEVICE, else the only online phone.</summary>
    /// <param name="idOrName">Device ID or name.</param>
    public Device Device(string? idOrName = null) => new(this, !string.IsNullOrEmpty(idOrName) ? idOrName : Env("DROIDLINE_DEVICE"));

    /// <summary>Send any command, including ones newer than this SDK.</summary>
    /// <param name="cmd">The command name.</param>
    /// <param name="parameters">Its parameters as an anonymous object or a dictionary, such as <c>new { device = "shelf-01" }</c>.</param>
    /// <param name="cancellationToken">Stops waiting for the reply. A command already sent may still run.</param>
    /// <returns>The reply without id and ok.</returns>
    public async Task<DroidlineObject> CallAsync(string cmd, object? parameters = null, CancellationToken cancellationToken = default)
    {
        var msg = Params.Message(cmd, null, null, Json.Fields(parameters));
        return DroidlineObject.FromReply<DroidlineObject>(await Connection.RequestAsync(msg, cancellationToken).ConfigureAwait(false));
    }

    /// <summary>Borrow a free phone so no other script can use it until it is released.</summary>
    /// <remarks>
    /// Optional: phones nobody leased keep working as before. Waits up to <paramref name="wait"/> seconds (default 30)
    /// for a match, else throws <see cref="NoFreeDeviceException"/>. The lease also ends after <paramref name="ttl"/>
    /// seconds (default 300) without a command. Disposing the returned device releases it.
    /// </remarks>
    /// <param name="device">This phone, by ID or name. Omit to take any free one.</param>
    /// <param name="wait">Seconds to wait for a phone to become free.</param>
    /// <param name="ttl">The lease ends after this many seconds without a command.</param>
    /// <param name="minSdk">Only phones with at least this Android API level, such as 33.</param>
    /// <param name="model">Only phones whose model contains this text.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public async Task<LeasedDevice> LeaseAsync(
        string? device = null, double? wait = null, double? ttl = null, int? minSdk = null, string? model = null, CancellationToken cancellationToken = default)
    {
        var args = new Params();
        args.Opt("device", device);
        args.Opt("wait", wait);
        args.Opt("ttl", ttl);
        args.Opt("min_sdk", minSdk);
        args.Opt("model", model);
        var reply = await Connection.RequestAsync(Params.Message("lease", null, null, args), cancellationToken).ConfigureAwait(false);
        var info = DroidlineObject.FromReply<DroidlineObject>(reply);
        return new LeasedDevice(this, info.Get<string>("device") ?? "", info.Get<string>("lease") ?? "", info);
    }

    /// <summary>Give a leased phone back. Releasing twice is harmless.</summary>
    /// <param name="lease">The lease that LeaseAsync returned.</param>
    /// <param name="device">Free this phone whatever lease holds it, for a script that crashed while holding it.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task ReleaseAsync(string? lease = null, string? device = null, CancellationToken cancellationToken = default)
    {
        var args = new Params();
        args.Opt("lease", lease);
        args.Opt("device", device);
        return Connection.RequestAsync(Params.Message("release", null, null, args), cancellationToken);
    }

    /// <summary>
    /// Call <paramref name="handler"/> for each event of this kind (device, notification, screen, toast, result,
    /// action) or every event with "*". Only result events arrive without SubscribeAsync.
    /// </summary>
    /// <remarks>Handlers run one at a time on the SDK's event thread. An exception from one is written to standard error.</remarks>
    /// <param name="kind">The event kind, or "*".</param>
    /// <param name="handler">Called with each event.</param>
    /// <returns>Dispose it to stop the handler.</returns>
    public IDisposable On(string kind, Action<DroidlineEvent> handler)
    {
        if (handler is null) throw new ArgumentNullException(nameof(handler));
        Connection.AddListener(kind, handler);
        return new Subscription(() => Connection.RemoveListener(kind, handler));
    }

    /// <summary>Stop calling a handler added with <see cref="On"/>.</summary>
    /// <param name="kind">The kind it was added for.</param>
    /// <param name="handler">The handler.</param>
    public void Off(string kind, Action<DroidlineEvent> handler) => Connection.RemoveListener(kind, handler);

    /// <summary>Close the socket. Calls still waiting fail with <see cref="ConnectionLostException"/>; a later call reconnects.</summary>
    public void Close() => Connection.Close();

    /// <summary>Closes the socket.</summary>
    public void Dispose() => Close();

    /// <summary>Closes the socket.</summary>
    public ValueTask DisposeAsync()
    {
        Close();
        return default;
    }

    /// <summary>The host and port.</summary>
    public override string ToString() => $"DroidlineClient({Host}, {Port})";

    /// <summary>Connect to the local Droidline server and return one phone.</summary>
    /// <remarks>
    /// Throws <see cref="ServerNotRunningException"/> right away if the server is not running. Disposing the device
    /// closes the connection this opened.
    /// </remarks>
    /// <param name="device">Device ID or name. Default: DROIDLINE_DEVICE, else the only online phone.</param>
    /// <param name="host">Server host. Default: DROIDLINE_HOST, else 127.0.0.1.</param>
    /// <param name="port">Client API port. Default: DROIDLINE_PORT, else 8780.</param>
    /// <param name="token">Client token. Default: DROIDLINE_TOKEN, else none.</param>
    /// <param name="cancellationToken">Stops connecting.</param>
    /// <example><code>await using var d = await DroidlineClient.ConnectAsync();</code></example>
    public static async Task<Device> ConnectAsync(
        string? device = null, string? host = null, int? port = null, string? token = null, CancellationToken cancellationToken = default)
    {
        var client = new DroidlineClient(host, port, token);
        try
        {
            await client.Connection.EnsureAsync(cancellationToken).ConfigureAwait(false);
        }
        catch
        {
            client.Close();
            throw;
        }
        var d = client.Device(device);
        d.OwnsClient = true;
        return d;
    }

    /// <summary>Like <see cref="ConnectAsync"/>, but borrows a free phone so no other script can use it.</summary>
    /// <remarks>Disposing the device releases the phone and closes the connection this opened.</remarks>
    /// <param name="device">This phone, by ID or name. Omit to take any free one.</param>
    /// <param name="wait">Seconds to wait for a phone to become free. Default: 30.</param>
    /// <param name="ttl">The lease ends after this many seconds without a command. Default: 300.</param>
    /// <param name="minSdk">Only phones with at least this Android API level.</param>
    /// <param name="model">Only phones whose model contains this text.</param>
    /// <param name="host">Server host. Default: DROIDLINE_HOST, else 127.0.0.1.</param>
    /// <param name="port">Client API port. Default: DROIDLINE_PORT, else 8780.</param>
    /// <param name="token">Client token. Default: DROIDLINE_TOKEN, else none.</param>
    /// <param name="cancellationToken">Stops waiting.</param>
    /// <example><code>await using var d = await DroidlineClient.LeaseAsync(wait: 60);</code></example>
    public static async Task<LeasedDevice> LeaseAsync(
        string? device = null, double? wait = null, double? ttl = null, int? minSdk = null, string? model = null,
        string? host = null, int? port = null, string? token = null, CancellationToken cancellationToken = default)
    {
        var client = new DroidlineClient(host, port, token);
        try
        {
            var d = await client.LeaseAsync(device, wait, ttl, minSdk, model, cancellationToken).ConfigureAwait(false);
            d.OwnsClient = true;
            return d;
        }
        catch
        {
            client.Close();
            throw;
        }
    }

    internal Task<JsonElement> RunAsync(string cmd, Params args, CancellationToken cancellationToken) =>
        Connection.RequestAsync(Params.Message(cmd, null, null, args), cancellationToken);

    internal async Task<T> RunValueAsync<T>(string cmd, Params args, CancellationToken cancellationToken) =>
        Json.ValueOf<T>(await RunAsync(cmd, args, cancellationToken).ConfigureAwait(false));

    internal async Task<T> RunFieldsAsync<T>(string cmd, Params args, CancellationToken cancellationToken)
        where T : DroidlineObject, new() =>
        DroidlineObject.FromReply<T>(await RunAsync(cmd, args, cancellationToken).ConfigureAwait(false));

    internal static string? Env(string name)
    {
        var value = Environment.GetEnvironmentVariable(name);
        return string.IsNullOrEmpty(value) ? null : value;
    }

    private static int? EnvPort()
    {
        var value = Env("DROIDLINE_PORT");
        if (value is null) return null;
        if (int.TryParse(value, NumberStyles.None, CultureInfo.InvariantCulture, out var port)) return port;
        throw new ArgumentException($"DROIDLINE_PORT is not a port number: {value}");
    }
}

internal sealed class Subscription : IDisposable
{
    private Action? _undo;

    public Subscription(Action undo)
    {
        _undo = undo;
    }

    public void Dispose() => Interlocked.Exchange(ref _undo, null)?.Invoke();
}
