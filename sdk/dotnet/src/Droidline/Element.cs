using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;

namespace Droidline;

/// <summary>One screen element, as it was when FindAsync or FindAllAsync returned it.</summary>
/// <remarks>
/// The fields are a snapshot. Actions look the element up again by its exact bounds and class, so after it moved or
/// disappeared they throw <see cref="NotFoundException"/>; call <see cref="RefreshAsync"/> or find it again. Actions do
/// not wait unless you pass a timeout.
/// </remarks>
public sealed class Element : IEquatable<Element>
{
    internal Element(Device device, Node node)
    {
        Device = device;
        Node = node;
    }

    /// <summary>The phone the element is on.</summary>
    public Device Device { get; }

    /// <summary>Every field the server sent for the element.</summary>
    public Node Node { get; }

    /// <summary>The text, or an empty string.</summary>
    public string Text => Field("text");

    /// <summary>The resource-id, or an empty string.</summary>
    public string Id => Field("id");

    /// <summary>The content-desc, or an empty string.</summary>
    public string Desc => Field("desc");

    /// <summary>The class name, such as android.widget.TextView, or an empty string.</summary>
    public string ClassName => Field("class");

    /// <summary>The package the element belongs to, or an empty string.</summary>
    public string Package => Field("package");

    /// <summary>The bounds in screen pixels.</summary>
    public (int Left, int Top, int Right, int Bottom) Bounds
    {
        get
        {
            var b = Node.Get<int[]>("bounds");
            if (b is null || b.Length < 4) b = new int[4];
            return (b[0], b[1], b[2], b[3]);
        }
    }

    /// <summary>The center point of the bounds.</summary>
    public (int X, int Y) Center
    {
        get
        {
            var (left, top, right, bottom) = Bounds;
            return ((int)Math.Floor((left + right) / 2.0), (int)Math.Floor((top + bottom) / 2.0));
        }
    }

    /// <summary>Any node field as a plain value, such as element["checked"] or element["scrollable"].</summary>
    /// <param name="field">The field name.</param>
    public object? this[string field] => Node[field];

    /// <summary>The query that finds this element again: its exact bounds and class.</summary>
    public Query ToQuery()
    {
        var (left, top, right, bottom) = Bounds;
        var q = new Query { Bounds = new[] { left, top, right, bottom } };
        if (ClassName.Length > 0) q.Class = ClassName;
        return q;
    }

    /// <summary>Tap the element.</summary>
    /// <param name="timeout">Seconds to wait for it. Default: 0.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<TouchResult> ClickAsync(double timeout = 0, CancellationToken cancellationToken = default) =>
        Device.TouchAsync(ToQuery(), timeout: timeout, cancellationToken: cancellationToken);

    /// <summary>Press and hold the element.</summary>
    /// <param name="ms">Hold time in milliseconds. Default: 800.</param>
    /// <param name="timeout">Seconds to wait for it. Default: 0.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<LongTouchResult> LongClickAsync(int? ms = null, double timeout = 0, CancellationToken cancellationToken = default) =>
        Device.LongTouchAsync(ToQuery(), ms, timeout: timeout, cancellationToken: cancellationToken);

    /// <summary>Set the element's text.</summary>
    /// <param name="text">Text to enter.</param>
    /// <param name="append">Keep existing text and add to the end. Default: false.</param>
    /// <param name="timeout">Seconds to wait for it. Default: 0.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<InputResult> InputAsync(string text, bool? append = null, double timeout = 0, CancellationToken cancellationToken = default) =>
        Device.InputAsync(ToQuery(), text, append, timeout: timeout, cancellationToken: cancellationToken);

    /// <summary>Empty the element's text.</summary>
    /// <param name="timeout">Seconds to wait for it. Default: 0.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<ClearResult> ClearAsync(double timeout = 0, CancellationToken cancellationToken = default) =>
        Device.ClearAsync(ToQuery(), timeout: timeout, cancellationToken: cancellationToken);

    /// <summary>Tap the center point, without looking the element up again.</summary>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<TapResult> TapAsync(CancellationToken cancellationToken = default)
    {
        var (x, y) = Center;
        return Device.TapAsync(x, y, cancellationToken);
    }

    /// <summary>Whether the element is still on screen, at the same bounds.</summary>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<bool> ExistsAsync(CancellationToken cancellationToken = default) => Device.ExistsAsync(ToQuery(), cancellationToken: cancellationToken);

    /// <summary>The same element with its current fields, for example after its text changed.</summary>
    /// <param name="timeout">Seconds to wait for it. Default: 0.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<Element> RefreshAsync(double timeout = 0, CancellationToken cancellationToken = default) =>
        Device.FindAsync(ToQuery(), timeout: timeout, cancellationToken: cancellationToken);

    /// <summary>An element inside this one.</summary>
    /// <param name="by">Which field to match, such as text or id.</param>
    /// <param name="value">The value to match.</param>
    /// <param name="nth">Index when several elements match, starting at 0.</param>
    /// <param name="timeout">Seconds to wait for it. Default: 10.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<Element> FindAsync(string by, string value, int? nth = null, double? timeout = null, CancellationToken cancellationToken = default) =>
        Device.FindAsync(Within(by, value), nth, timeout, cancellationToken);

    /// <summary>An element inside this one.</summary>
    /// <param name="query">A query object; inside is added to it.</param>
    /// <param name="nth">Index when several elements match, starting at 0.</param>
    /// <param name="timeout">Seconds to wait for it. Default: 10.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<Element> FindAsync(object query, int? nth = null, double? timeout = null, CancellationToken cancellationToken = default) =>
        Device.FindAsync(Within(query, null), nth, timeout, cancellationToken);

    /// <summary>Every element inside this one that matches.</summary>
    /// <param name="by">Which field to match, such as text or id.</param>
    /// <param name="value">The value to match.</param>
    /// <param name="timeout">Seconds to wait for at least one match. Default: 0.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<IReadOnlyList<Element>> FindAllAsync(string by, string value, double? timeout = null, CancellationToken cancellationToken = default) =>
        Device.FindAllAsync(Within(by, value), timeout, cancellationToken);

    /// <summary>Every element inside this one that matches.</summary>
    /// <param name="query">A query object; inside is added to it.</param>
    /// <param name="timeout">Seconds to wait for at least one match. Default: 0.</param>
    /// <param name="cancellationToken">Stops waiting for the reply.</param>
    public Task<IReadOnlyList<Element>> FindAllAsync(object query, double? timeout = null, CancellationToken cancellationToken = default) =>
        Device.FindAllAsync(Within(query, null), timeout, cancellationToken);

    private Dictionary<string, object?> Within(object by, string? value)
    {
        var q = by is string field
            ? new Dictionary<string, object?>(StringComparer.Ordinal) { [field] = value }
            : Json.Normalize(by) as Dictionary<string, object?> ?? throw new ArgumentException("A query must be an object.", nameof(by));
        if (q.ContainsKey("inside")) throw new ArgumentException("This query already has inside; call FindAsync on the device with your own query instead.", nameof(by));
        q["inside"] = ToQuery();
        return q;
    }

    private string Field(string name) => Node.Get<string>(name) ?? "";

    /// <summary>True when both have the same bounds and class.</summary>
    /// <param name="other">The other element.</param>
    public bool Equals(Element? other) => other is not null && other.Bounds == Bounds && other.ClassName == ClassName;

    /// <summary>True when the other object is an element with the same bounds and class.</summary>
    /// <param name="obj">The other object.</param>
    public override bool Equals(object? obj) => Equals(obj as Element);

    /// <summary>A hash of the bounds and class.</summary>
    public override int GetHashCode()
    {
        unchecked
        {
            var (left, top, right, bottom) = Bounds;
            var hash = 17;
            foreach (var n in new[] { left, top, right, bottom }) hash = hash * 31 + n;
            return hash * 31 + StringComparer.Ordinal.GetHashCode(ClassName);
        }
    }

    /// <summary>The class, a label and the bounds.</summary>
    public override string ToString()
    {
        var label = Text.Length > 0 ? Text : Desc.Length > 0 ? Desc : Id;
        var (left, top, right, bottom) = Bounds;
        return $"Element({(ClassName.Length > 0 ? ClassName : "?")} {Json.Serialize(label)} [{left}, {top}, {right}, {bottom}])";
    }
}
