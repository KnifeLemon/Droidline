using System;
using System.Collections.Generic;

namespace Droidline;

/// <summary>The named parameters of one request, in the order they go on the wire.</summary>
internal sealed class Params : Dictionary<string, object?>
{
    public Params()
        : base(StringComparer.Ordinal)
    {
    }

    /// <summary>Adds an optional parameter; null leaves it out so the server uses its default.</summary>
    public void Opt(string name, object? value)
    {
        if (value is not null) this[name] = value;
    }

    public static Params Message(string cmd, string? device, string? lease, IEnumerable<KeyValuePair<string, object?>> args)
    {
        var msg = new Params { ["cmd"] = cmd };
        if (device is not null) msg["device"] = device;
        if (lease is not null) msg["lease"] = lease;
        foreach (var kv in args) msg[kv.Key] = kv.Value;
        return msg;
    }
}
