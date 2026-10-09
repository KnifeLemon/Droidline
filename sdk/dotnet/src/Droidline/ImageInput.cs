using System;
using System.IO;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

namespace Droidline;

/// <summary>
/// A PNG or JPEG for FindImageAsync and TapImageAsync: a file path or the image bytes. A string or a byte array
/// converts to it, so <c>d.TapImageAsync("ok-button.png")</c> works.
/// </summary>
public readonly struct ImageInput
{
    private readonly string? _path;
    private readonly byte[]? _bytes;

    private ImageInput(string? path, byte[]? bytes)
    {
        _path = path;
        _bytes = bytes;
    }

    /// <summary>An image read from this file when the command is sent.</summary>
    /// <param name="path">Path to a PNG or JPEG file.</param>
    public static ImageInput FromFile(string path) => new(path ?? throw new ArgumentNullException(nameof(path)), null);

    /// <summary>An image from bytes already in memory.</summary>
    /// <param name="bytes">The PNG or JPEG bytes.</param>
    public static ImageInput FromBytes(byte[] bytes) => new(null, bytes ?? throw new ArgumentNullException(nameof(bytes)));

    /// <summary>An image read from this file.</summary>
    /// <param name="path">Path to a PNG or JPEG file.</param>
    public static implicit operator ImageInput(string path) => FromFile(path);

    /// <summary>An image from these bytes.</summary>
    /// <param name="bytes">The PNG or JPEG bytes.</param>
    public static implicit operator ImageInput(byte[] bytes) => FromBytes(bytes);

    internal async Task<string> ToBase64Async(CancellationToken cancellationToken)
    {
        if (_bytes is not null) return Convert.ToBase64String(_bytes);
        if (_path is null) throw new ArgumentException("The image has neither a path nor bytes.");
        return Convert.ToBase64String(await Files.ReadAllBytesAsync(_path, cancellationToken).ConfigureAwait(false));
    }
}

internal static class Files
{
    private static readonly Encoding Utf8 = new UTF8Encoding(false);

    public static async Task<byte[]> ReadAllBytesAsync(string path, CancellationToken cancellationToken)
    {
        using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read, 4096, useAsync: true);
        using var buffer = new MemoryStream();
        await stream.CopyToAsync(buffer, 81920, cancellationToken).ConfigureAwait(false);
        return buffer.ToArray();
    }

    public static async Task WriteAllBytesAsync(string path, byte[] bytes, CancellationToken cancellationToken)
    {
        using var stream = new FileStream(path, FileMode.Create, FileAccess.Write, FileShare.None, 4096, useAsync: true);
        await stream.WriteAsync(bytes, 0, bytes.Length, cancellationToken).ConfigureAwait(false);
    }

    public static Task WriteAllTextAsync(string path, string text, CancellationToken cancellationToken) =>
        WriteAllBytesAsync(path, Utf8.GetBytes(text), cancellationToken);
}
