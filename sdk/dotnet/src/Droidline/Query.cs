using System;
using System.Collections.Generic;

namespace Droidline;

/// <example>
/// <code>
/// await d.TouchAsync(new Query { Class = "android.widget.Switch", Row = new Query { Text = "Wi-Fi" } });
/// </code>
/// </example>
public sealed partial class Query : Dictionary<string, object?>
{
    /// <summary>Creates an empty query. Set its properties, or add any key with the indexer.</summary>
    public Query()
        : base(StringComparer.Ordinal)
    {
    }

    private T? Read<T>(string key) => TryGetValue(key, out var value) && value is T typed ? typed : default;

    private void Write(string key, object? value)
    {
        if (value is null) Remove(key);
        else this[key] = value;
    }
}
