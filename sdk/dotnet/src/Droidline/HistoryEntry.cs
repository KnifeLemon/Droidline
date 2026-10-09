using System;
using System.Collections.Generic;

namespace Droidline;

/// <summary>One request in the history each client keeps of its last 200 requests.</summary>
public sealed class HistoryEntry
{
    internal HistoryEntry(DateTimeOffset time, string? device, string cmd, IReadOnlyDictionary<string, object?> parameters, string? error, long ms)
    {
        Time = time;
        Device = device;
        Cmd = cmd;
        Params = parameters;
        Error = error;
        Ms = ms;
    }

    /// <summary>When the request started.</summary>
    public DateTimeOffset Time { get; }

    /// <summary>The device ID or name the request named, or null for server commands and the only online phone.</summary>
    public string? Device { get; }

    /// <summary>The command, such as touch.</summary>
    public string Cmd { get; }

    /// <summary>The parameters as plain values, without the token. Strings over 200 characters are shortened to their length.</summary>
    public IReadOnlyDictionary<string, object?> Params { get; }

    /// <summary>True when the server answered ok.</summary>
    public bool Ok => Error is null;

    /// <summary>The error code when the request failed, such as NOT_FOUND.</summary>
    public string? Error { get; }

    /// <summary>Milliseconds until the reply or the failure.</summary>
    public long Ms { get; }
}
