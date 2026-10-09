using System;
using System.Collections;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;

namespace Droidline;

/// <summary>
/// A JSON object from the server: a reply, an event, an element or an error's extra fields. Subclasses add typed
/// properties for the fields the spec names; the indexer and <see cref="Get{T}(string)"/> read any field, including
/// ones newer than this SDK.
/// </summary>
/// <remarks>
/// The indexer gives plain values: string, long, double, bool, null, a nested <see cref="DroidlineObject"/> or a list
/// of these. <see cref="Get{T}(string)"/> converts a field to the type you ask for, such as
/// <c>Get&lt;int[]&gt;("bounds")</c>.
/// </remarks>
public class DroidlineObject : IReadOnlyDictionary<string, object?>
{
    private static readonly string[] ReplyEnvelope = { "id", "ok" };

    private readonly Dictionary<string, JsonElement> _fields = new(StringComparer.Ordinal);

    /// <summary>Creates an empty object. Each field is read from JSON that the SDK received.</summary>
    public DroidlineObject()
    {
    }

    internal IEnumerable<KeyValuePair<string, JsonElement>> Elements => _fields;

    internal void Load(JsonElement obj, ICollection<string>? skip = null)
    {
        if (obj.ValueKind != JsonValueKind.Object) return;
        foreach (var prop in obj.EnumerateObject())
        {
            if (skip is not null && skip.Contains(prop.Name)) continue;
            _fields[prop.Name] = prop.Value.Clone();
        }
    }

    internal static T FromReply<T>(JsonElement reply) where T : DroidlineObject, new()
    {
        var obj = new T();
        obj.Load(reply, ReplyEnvelope);
        return obj;
    }

    internal T As<T>() where T : DroidlineObject, new()
    {
        var obj = new T();
        foreach (var kv in _fields) obj._fields[kv.Key] = kv.Value;
        return obj;
    }

    /// <summary>A field as a plain value. Throws <see cref="KeyNotFoundException"/> when the field is missing.</summary>
    /// <param name="field">The field name as the server sends it, such as "checked".</param>
    public object? this[string field] => Json.ToPlain(_fields[field]);

    /// <summary>A field converted to <typeparamref name="T"/>, or the default of T when it is missing or null.</summary>
    /// <param name="field">The field name as the server sends it, such as "bounds".</param>
    public T? Get<T>(string field) => _fields.TryGetValue(field, out var element) ? Json.Convert<T>(element) : default;

    /// <summary>True when the server sent this field.</summary>
    /// <param name="field">The field name.</param>
    public bool Has(string field) => _fields.ContainsKey(field);

    /// <summary>The object as JSON.</summary>
    /// <param name="indented">Indent with two spaces instead of writing one line.</param>
    public string ToJson(bool indented = false) => Json.Serialize(this, indented ? JsonStyle.Indented : JsonStyle.Compact);

    /// <summary>The object as one line of JSON.</summary>
    public override string ToString() => ToJson();

    /// <summary>The number of fields.</summary>
    public int Count => _fields.Count;

    /// <summary>The field names.</summary>
    public IEnumerable<string> Keys => _fields.Keys;

    /// <summary>The field values as plain values.</summary>
    public IEnumerable<object?> Values => _fields.Values.Select(Json.ToPlain);

    /// <summary>True when the server sent this field.</summary>
    /// <param name="key">The field name.</param>
    public bool ContainsKey(string key) => _fields.ContainsKey(key);

    /// <summary>Reads a field as a plain value.</summary>
    /// <param name="key">The field name.</param>
    /// <param name="value">The plain value, or null when the field is missing.</param>
    public bool TryGetValue(string key, out object? value)
    {
        if (_fields.TryGetValue(key, out var element))
        {
            value = Json.ToPlain(element);
            return true;
        }
        value = null;
        return false;
    }

    /// <summary>Enumerates the fields as plain values.</summary>
    public IEnumerator<KeyValuePair<string, object?>> GetEnumerator() =>
        _fields.Select(kv => new KeyValuePair<string, object?>(kv.Key, Json.ToPlain(kv.Value))).GetEnumerator();

    IEnumerator IEnumerable.GetEnumerator() => GetEnumerator();
}

/// <summary>An event from the server, such as a notification, a screen change or a late batch result.</summary>
public sealed class DroidlineEvent : DroidlineObject
{
    /// <summary>The event kind: device, notification, screen, toast, result or action.</summary>
    public string Kind => Get<string>("event") ?? "";

    /// <summary>The ID of the phone the event came from, when it has one.</summary>
    public string? Device => Get<string>("device");
}
