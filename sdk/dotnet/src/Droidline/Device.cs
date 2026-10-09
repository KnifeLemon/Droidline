using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;

namespace Droidline;

/// <summary>One phone. Every command method sends a request with this device's ID or name.</summary>
/// <remarks>
/// Get one from <see cref="DroidlineClient.ConnectAsync"/> or <see cref="DroidlineClient.Device(string)"/>. Disposing
/// a device from ConnectAsync closes its connection; disposing one from <c>client.Device()</c> does nothing.
/// </remarks>
public partial class Device : IDisposable, IAsyncDisposable
{
    internal Device(DroidlineClient client, string? id)
    {
        Client = client;
        Id = id;
    }

    /// <summary>The client this device sends its requests through.</summary>
    public DroidlineClient Client { get; }

    /// <summary>The device ID or name sent with each request, or null for the only online phone.</summary>
    public string? Id { get; }

    /// <summary>Set while this handle holds a lease; every request carries it.</summary>
    public string? LeaseId { get; internal set; }

    internal bool OwnsClient { get; set; }

    /// <summary>Recent requests to this phone, oldest first.</summary>
    public IReadOnlyList<HistoryEntry> History => Client.History.Where(e => Id is null || e.Device == Id).ToList();

    /// <summary>Send any command to this phone, including ones newer than this SDK.</summary>
    /// <param name="cmd">The command name.</param>
    /// <param name="parameters">Its parameters as an anonymous object or a dictionary, such as <c>new { by = "text", value = "OK" }</c>.</param>
    /// <param name="cancellationToken">Stops waiting for the reply. A command already sent may still run on the phone.</param>
    /// <returns>The reply without id and ok.</returns>
    public async Task<DroidlineObject> CallAsync(string cmd, object? parameters = null, CancellationToken cancellationToken = default)
    {
        var msg = Params.Message(cmd, Id, LeaseId, Json.Fields(parameters));
        return DroidlineObject.FromReply<DroidlineObject>(await Client.Connection.RequestAsync(msg, cancellationToken).ConfigureAwait(false));
    }

    /// <summary>Call <paramref name="callback"/> for every matching notification from this phone.</summary>
    /// <remarks>
    /// The phone forwards only the apps allowed with NotifyFilterAsync. The callback runs on the SDK's event thread.
    /// </remarks>
    /// <param name="callback">Called with each notification.</param>
    /// <param name="package">Only from this app.</param>
    /// <param name="textContains">Only if title or text contains this.</param>
    /// <param name="cancellationToken">Stops waiting for the subscribe reply.</param>
    /// <returns>Dispose it to stop the callback.</returns>
    /// <example><code>using var stop = await d.OnNotificationAsync(n => Console.WriteLine(n.Text), package: "com.kakao.talk");</code></example>
    public async Task<IDisposable> OnNotificationAsync(
        Action<Notification> callback, string? package = null, string? textContains = null, CancellationToken cancellationToken = default)
    {
        if (callback is null) throw new ArgumentNullException(nameof(callback));
        var ids = await IdentitiesAsync(cancellationToken).ConfigureAwait(false);

        void Handler(DroidlineEvent ev)
        {
            if (ids.Count > 0 && !ids.Contains(ev.Get<string>("device") ?? "") && !ids.Contains(ev.Get<string>("name") ?? "")) return;
            if (package is not null && ev.Get<string>("package") != package) return;
            if (textContains is not null && !Text(ev, "title").Contains(textContains) && !Text(ev, "text").Contains(textContains)) return;
            callback(ev.As<Notification>());
        }

        var connection = Client.Connection;
        Action<DroidlineEvent> handler = Handler;
        connection.AddListener("notification", handler);
        try
        {
            var subscribe = new Params { ["cmd"] = "subscribe", ["events"] = new[] { "notification" } };
            await connection.SubscribeOnceAsync(subscribe, cancellationToken).ConfigureAwait(false);
        }
        catch
        {
            connection.RemoveListener("notification", handler);
            throw;
        }
        return new Subscription(() => connection.RemoveListener("notification", handler));
    }

    private static string Text(DroidlineObject ev, string field) =>
        ev.TryGetValue(field, out var value) ? Convert.ToString(value, CultureInfo.InvariantCulture) ?? "" : "";

    // Events name the device by ID; resolve a name so both match. Empty means any device.
    private async Task<HashSet<string>> IdentitiesAsync(CancellationToken cancellationToken)
    {
        var ids = new HashSet<string>(StringComparer.Ordinal);
        if (Id is null) return ids;
        ids.Add(Id);
        try
        {
            foreach (var info in await Client.DevicesAsync(cancellationToken).ConfigureAwait(false))
            {
                var id = info.Get<string>("id");
                var name = info.Get<string>("name");
                if (Id != id && Id != name) continue;
                if (!string.IsNullOrEmpty(id)) ids.Add(id!);
                if (!string.IsNullOrEmpty(name)) ids.Add(name!);
            }
        }
        catch (DroidlineException)
        {
        }
        return ids;
    }

    /// <summary>Close the connection if ConnectAsync opened it for this device; otherwise do nothing.</summary>
    public virtual void Close()
    {
        if (OwnsClient) Client.Close();
    }

    /// <summary>Same as <see cref="Close"/>.</summary>
    public void Dispose() => Close();

    /// <summary>Same as <see cref="Close"/>.</summary>
    public virtual ValueTask DisposeAsync()
    {
        Close();
        return default;
    }

    /// <summary>The device ID or name.</summary>
    public override string ToString() => $"Device({Id})";

    internal Task<JsonElement> RunAsync(string cmd, Params args, CancellationToken cancellationToken) =>
        Client.Connection.RequestAsync(Params.Message(cmd, Id, LeaseId, args), cancellationToken);

    internal async Task<T> RunValueAsync<T>(string cmd, Params args, CancellationToken cancellationToken) =>
        Json.ValueOf<T>(await RunAsync(cmd, args, cancellationToken).ConfigureAwait(false));

    internal async Task<T> RunFieldsAsync<T>(string cmd, Params args, CancellationToken cancellationToken)
        where T : DroidlineObject, new() =>
        DroidlineObject.FromReply<T>(await RunAsync(cmd, args, cancellationToken).ConfigureAwait(false));

    internal async Task<Element> RunElementAsync(string cmd, Params args, CancellationToken cancellationToken)
    {
        var node = await RunValueAsync<Node>(cmd, args, cancellationToken).ConfigureAwait(false);
        return new Element(this, node ?? new Node());
    }

    internal async Task<IReadOnlyList<Element>> RunElementsAsync(string cmd, Params args, CancellationToken cancellationToken)
    {
        var nodes = await RunValueAsync<IReadOnlyList<Node>>(cmd, args, cancellationToken).ConfigureAwait(false);
        return (nodes ?? Array.Empty<Node>()).Select(n => new Element(this, n)).ToList();
    }

    internal async Task<byte[]> SaveImageAsync(string cmd, string? path, Params args, CancellationToken cancellationToken)
    {
        if (path is not null && !args.ContainsKey("format"))
        {
            var ext = Path.GetExtension(path).ToLowerInvariant();
            args.Opt("format", ext == ".png" ? "png" : ext is ".jpg" or ".jpeg" ? "jpeg" : null);
        }
        var reply = await RunFieldsAsync<DroidlineObject>(cmd, args, cancellationToken).ConfigureAwait(false);
        var image = Convert.FromBase64String(reply.Get<string>("data") ?? "");
        if (path is not null) await Files.WriteAllBytesAsync(path, image, cancellationToken).ConfigureAwait(false);
        return image;
    }

    internal async Task<T> SaveJsonAsync<T>(string cmd, string? path, Params args, CancellationToken cancellationToken)
        where T : DroidlineObject, new()
    {
        var result = await RunFieldsAsync<T>(cmd, args, cancellationToken).ConfigureAwait(false);
        if (path is not null) await Files.WriteAllTextAsync(path, result.ToJson(indented: true), cancellationToken).ConfigureAwait(false);
        return result;
    }
}

/// <summary>A phone borrowed with LeaseAsync. Every request carries the lease; disposing it gives the phone back.</summary>
public sealed class LeasedDevice : Device
{
    internal LeasedDevice(DroidlineClient client, string device, string leaseId, DroidlineObject info)
        : base(client, device)
    {
        LeaseId = leaseId;
        var name = info.Get<string>("name");
        Name = string.IsNullOrEmpty(name) ? device : name!;
        Ttl = info.Get<double>("ttl");
    }

    /// <summary>The phone's name, or its ID when it has none.</summary>
    public string Name { get; }

    /// <summary>Seconds without a command after which the server ends the lease.</summary>
    public double Ttl { get; }

    /// <summary>Give the phone back. Releasing twice is harmless.</summary>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public async Task ReleaseAsync(CancellationToken cancellationToken = default)
    {
        var id = LeaseId;
        LeaseId = null;
        if (id is not null) await Client.ReleaseAsync(id, cancellationToken: cancellationToken).ConfigureAwait(false);
    }

    /// <summary>Release the phone, then close the connection if LeaseAsync opened it.</summary>
    public override void Close()
    {
        try
        {
            ReleaseAsync().ConfigureAwait(false).GetAwaiter().GetResult();
        }
        finally
        {
            base.Close();
        }
    }

    /// <summary>Release the phone, then close the connection if LeaseAsync opened it.</summary>
    public override async ValueTask DisposeAsync()
    {
        try
        {
            await ReleaseAsync().ConfigureAwait(false);
        }
        finally
        {
            base.Close();
        }
    }

    /// <summary>The name and lease.</summary>
    public override string ToString() => $"LeasedDevice({Name}, lease={LeaseId})";
}
