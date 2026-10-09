using System;
using System.Collections;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Reflection;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Droidline;

internal enum JsonStyle
{
    // One line, as on the wire.
    Compact,
    // ", " and ": " between items, like Python's json.dumps, for the command log.
    Spaced,
    // Two-space indents, for files written by DumpAsync.
    Indented,
}

/// <summary>
/// Turns caller values into JSON and server JSON into .NET values. Anonymous objects keep their property names as
/// they are, value tuples become arrays like Python tuples, and no naming policy is applied anywhere.
/// </summary>
internal static class Json
{
    private const int MaxDepth = 64;

    public static string Serialize(object? value, JsonStyle style = JsonStyle.Compact)
    {
        var sb = new StringBuilder();
        Write(sb, Normalize(value), style, 0);
        return sb.ToString();
    }

    /// <summary>
    /// A plain tree of the value: null, string, bool, long, ulong, double, decimal,
    /// <c>Dictionary&lt;string, object?&gt;</c> and <c>List&lt;object?&gt;</c>.
    /// </summary>
    public static object? Normalize(object? value) => Normalize(value, 0);

    /// <summary>The fields of an anonymous object or dictionary passed as command parameters.</summary>
    public static IEnumerable<KeyValuePair<string, object?>> Fields(object? parameters)
    {
        if (parameters is null) return Array.Empty<KeyValuePair<string, object?>>();
        if (Normalize(parameters) is Dictionary<string, object?> fields) return fields;
        throw new ArgumentException("Parameters must be an object or a dictionary, such as new { foo = 1 }.", nameof(parameters));
    }

    private static object? Normalize(object? value, int depth)
    {
        if (depth > MaxDepth) throw new ArgumentException("The value is nested too deeply to send as JSON.");
        switch (value)
        {
            case null:
                return null;
            case string s:
                return s;
            case char c:
                return c.ToString();
            case bool b:
                return b;
            case int or long or short or sbyte or byte or ushort or uint:
                return System.Convert.ToInt64(value, CultureInfo.InvariantCulture);
            case ulong u:
                return u;
            case float f:
                return (double)f;
            case double d:
                return d;
            case decimal m:
                return m;
            case Enum e:
                return e.ToString();
            case JsonElement element:
                return NormalizeElement(element);
            case DroidlineObject obj:
                return obj.Elements.ToDictionary(kv => kv.Key, kv => NormalizeElement(kv.Value), StringComparer.Ordinal);
            case byte[] bytes:
                return System.Convert.ToBase64String(bytes);
            case IDictionary dict:
            {
                var result = new Dictionary<string, object?>(StringComparer.Ordinal);
                foreach (DictionaryEntry entry in dict)
                {
                    result[System.Convert.ToString(entry.Key, CultureInfo.InvariantCulture) ?? ""] = Normalize(entry.Value, depth + 1);
                }
                return result;
            }
            case IEnumerable<KeyValuePair<string, object?>> pairs:
                return pairs.ToDictionary(kv => kv.Key, kv => Normalize(kv.Value, depth + 1), StringComparer.Ordinal);
        }

        var type = value.GetType();
        if (IsTuple(type)) return TupleItems(value).Select(item => Normalize(item, depth + 1)).ToList();
        var pairType = KeyValuePairsOf(type);
        if (pairType is not null)
        {
            var key = pairType.GetProperty("Key")!;
            var val = pairType.GetProperty("Value")!;
            var result = new Dictionary<string, object?>(StringComparer.Ordinal);
            foreach (var item in (IEnumerable)value) result[(string)key.GetValue(item)!] = Normalize(val.GetValue(item), depth + 1);
            return result;
        }
        if (value is IEnumerable list) return list.Cast<object?>().Select(item => Normalize(item, depth + 1)).ToList();

        var fields = new Dictionary<string, object?>(StringComparer.Ordinal);
        foreach (var prop in type.GetProperties(BindingFlags.Public | BindingFlags.Instance))
        {
            if (!prop.CanRead || prop.GetIndexParameters().Length > 0) continue;
            var ignore = prop.GetCustomAttribute<JsonIgnoreAttribute>();
            if (ignore is not null && ignore.Condition == JsonIgnoreCondition.Always) continue;
            var name = prop.GetCustomAttribute<JsonPropertyNameAttribute>()?.Name ?? prop.Name;
            fields[name] = Normalize(prop.GetValue(value), depth + 1);
        }
        return fields;
    }

    private static bool IsTuple(Type type)
    {
        if (!type.IsGenericType) return false;
        var name = type.GetGenericTypeDefinition().FullName ?? "";
        return name.StartsWith("System.ValueTuple`", StringComparison.Ordinal) || name.StartsWith("System.Tuple`", StringComparison.Ordinal);
    }

    private static IEnumerable<object?> TupleItems(object tuple)
    {
        var type = tuple.GetType();
        for (var i = 1; i <= 7; i++)
        {
            var name = "Item" + i.ToString(CultureInfo.InvariantCulture);
            var field = type.GetField(name);
            var prop = field is null ? type.GetProperty(name) : null;
            if (field is null && prop is null) yield break;
            yield return field is not null ? field.GetValue(tuple) : prop!.GetValue(tuple);
        }
        var rest = (object?)type.GetField("Rest")?.GetValue(tuple) ?? type.GetProperty("Rest")?.GetValue(tuple);
        if (rest is null) yield break;
        foreach (var item in TupleItems(rest)) yield return item;
    }

    private static Type? KeyValuePairsOf(Type type)
    {
        foreach (var iface in type.GetInterfaces())
        {
            if (!iface.IsGenericType || iface.GetGenericTypeDefinition() != typeof(IEnumerable<>)) continue;
            var item = iface.GetGenericArguments()[0];
            if (item.IsGenericType && item.GetGenericTypeDefinition() == typeof(KeyValuePair<,>) && item.GetGenericArguments()[0] == typeof(string))
            {
                return item;
            }
        }
        return null;
    }

    private static object? NormalizeElement(JsonElement element)
    {
        switch (element.ValueKind)
        {
            case JsonValueKind.Object:
                var obj = new Dictionary<string, object?>(StringComparer.Ordinal);
                foreach (var prop in element.EnumerateObject()) obj[prop.Name] = NormalizeElement(prop.Value);
                return obj;
            case JsonValueKind.Array:
                return element.EnumerateArray().Select(NormalizeElement).ToList();
            case JsonValueKind.String:
                return element.GetString();
            case JsonValueKind.Number:
                if (element.TryGetInt64(out var l)) return l;
                if (element.TryGetUInt64(out var u)) return u;
                return element.GetDouble();
            case JsonValueKind.True:
                return true;
            case JsonValueKind.False:
                return false;
            default:
                return null;
        }
    }

    private static void Write(StringBuilder sb, object? value, JsonStyle style, int depth)
    {
        switch (value)
        {
            case null:
                sb.Append("null");
                return;
            case string s:
                WriteString(sb, s);
                return;
            case bool b:
                sb.Append(b ? "true" : "false");
                return;
            case long l:
                sb.Append(l.ToString(CultureInfo.InvariantCulture));
                return;
            case ulong u:
                sb.Append(u.ToString(CultureInfo.InvariantCulture));
                return;
            case double d:
                if (double.IsNaN(d) || double.IsInfinity(d)) throw new ArgumentException("JSON has no NaN or Infinity.");
                sb.Append(d.ToString("R", CultureInfo.InvariantCulture));
                return;
            case decimal m:
                sb.Append(m.ToString(CultureInfo.InvariantCulture));
                return;
            case Dictionary<string, object?> obj:
                WriteContainer(sb, '{', '}', obj.Count, obj.Select(kv => ((string?)kv.Key, kv.Value)), style, depth, keyed: true);
                return;
            case List<object?> list:
                WriteContainer(sb, '[', ']', list.Count, list.Select(v => ((string?)null, v)), style, depth, keyed: false);
                return;
            default:
                throw new ArgumentException($"Cannot write {value.GetType()} as JSON.");
        }
    }

    private static void WriteContainer(
        StringBuilder sb, char open, char close, int count, IEnumerable<(string? Key, object? Value)> items, JsonStyle style, int depth, bool keyed)
    {
        sb.Append(open);
        if (count == 0)
        {
            sb.Append(close);
            return;
        }
        var first = true;
        foreach (var (key, item) in items)
        {
            if (!first) sb.Append(style == JsonStyle.Spaced ? ", " : ",");
            first = false;
            if (style == JsonStyle.Indented) sb.Append('\n').Append(' ', (depth + 1) * 2);
            if (keyed)
            {
                WriteString(sb, key!);
                sb.Append(style == JsonStyle.Compact ? ":" : ": ");
            }
            Write(sb, item, style, depth + 1);
        }
        if (style == JsonStyle.Indented) sb.Append('\n').Append(' ', depth * 2);
        sb.Append(close);
    }

    private static void WriteString(StringBuilder sb, string s)
    {
        sb.Append('"');
        foreach (var ch in s)
        {
            switch (ch)
            {
                case '"': sb.Append("\\\""); break;
                case '\\': sb.Append("\\\\"); break;
                case '\b': sb.Append("\\b"); break;
                case '\f': sb.Append("\\f"); break;
                case '\n': sb.Append("\\n"); break;
                case '\r': sb.Append("\\r"); break;
                case '\t': sb.Append("\\t"); break;
                default:
                    if (ch < 0x20) sb.Append("\\u").Append(((int)ch).ToString("x4", CultureInfo.InvariantCulture));
                    else sb.Append(ch);
                    break;
            }
        }
        sb.Append('"');
    }

    /// <summary>A JSON value as a plain .NET value; objects become <see cref="DroidlineObject"/>.</summary>
    public static object? ToPlain(JsonElement element)
    {
        switch (element.ValueKind)
        {
            case JsonValueKind.Object:
                var obj = new DroidlineObject();
                obj.Load(element);
                return obj;
            case JsonValueKind.Array:
                return element.EnumerateArray().Select(ToPlain).ToList();
            case JsonValueKind.String:
                return element.GetString();
            case JsonValueKind.Number:
                if (element.TryGetInt64(out var l)) return l;
                return element.GetDouble();
            case JsonValueKind.True:
                return true;
            case JsonValueKind.False:
                return false;
            default:
                return null;
        }
    }

    public static T ValueOf<T>(JsonElement reply) =>
        reply.ValueKind == JsonValueKind.Object && reply.TryGetProperty("value", out var value) ? Convert<T>(value) : default!;

    public static T Convert<T>(JsonElement element) => Convert(element, typeof(T)) is T result ? result : default!;

    public static object? Convert(JsonElement element, Type type)
    {
        if (type == typeof(JsonElement)) return element.Clone();
        if (element.ValueKind is JsonValueKind.Null or JsonValueKind.Undefined) return null;
        type = Nullable.GetUnderlyingType(type) ?? type;
        if (type == typeof(object)) return ToPlain(element);
        if (type == typeof(string)) return element.ValueKind == JsonValueKind.String ? element.GetString() : element.GetRawText();
        if (type == typeof(bool)) return element.GetBoolean();
        if (type == typeof(int)) return element.TryGetInt32(out var i) ? i : checked((int)element.GetDouble());
        if (type == typeof(long)) return element.TryGetInt64(out var l) ? l : checked((long)element.GetDouble());
        if (type == typeof(double)) return element.GetDouble();
        if (type == typeof(float)) return element.GetSingle();
        if (type == typeof(decimal)) return element.GetDecimal();
        if (typeof(DroidlineObject).IsAssignableFrom(type))
        {
            var obj = (DroidlineObject)Activator.CreateInstance(type, nonPublic: true)!;
            if (element.ValueKind == JsonValueKind.Object) obj.Load(element);
            return obj;
        }
        if (type.IsArray && element.ValueKind == JsonValueKind.Array)
        {
            var itemType = type.GetElementType()!;
            var items = element.EnumerateArray().ToList();
            var array = Array.CreateInstance(itemType, items.Count);
            for (var k = 0; k < items.Count; k++) array.SetValue(Convert(items[k], itemType), k);
            return array;
        }
        if (type.IsGenericType)
        {
            var def = type.GetGenericTypeDefinition();
            var args = type.GetGenericArguments();
            if (element.ValueKind == JsonValueKind.Array && ListTypes.Contains(def))
            {
                var list = (IList)Activator.CreateInstance(typeof(List<>).MakeGenericType(args[0]))!;
                foreach (var item in element.EnumerateArray()) list.Add(Convert(item, args[0]));
                return list;
            }
            if (element.ValueKind == JsonValueKind.Object && DictionaryTypes.Contains(def) && args[0] == typeof(string))
            {
                var dict = (IDictionary)Activator.CreateInstance(typeof(Dictionary<,>).MakeGenericType(args))!;
                foreach (var prop in element.EnumerateObject()) dict[prop.Name] = Convert(prop.Value, args[1]);
                return dict;
            }
        }
        return JsonSerializer.Deserialize(element.GetRawText(), type);
    }

    private static readonly HashSet<Type> ListTypes = new()
    {
        typeof(IEnumerable<>), typeof(IReadOnlyList<>), typeof(IReadOnlyCollection<>), typeof(IList<>), typeof(ICollection<>), typeof(List<>),
    };

    private static readonly HashSet<Type> DictionaryTypes = new()
    {
        typeof(IReadOnlyDictionary<,>), typeof(IDictionary<,>), typeof(Dictionary<,>),
    };
}
