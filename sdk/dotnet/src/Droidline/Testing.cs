using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;

namespace Droidline;

/// <summary>Helpers for test suites, usable with any test framework.</summary>
/// <example>
/// <code>
/// try
/// {
///     await RunStepsAsync(d);
/// }
/// catch
/// {
///     await Testing.SaveFailureAsync(d, "artifacts/login");
///     throw;
/// }
/// </code>
/// </example>
public static class Testing
{
    /// <summary>The command log as text, one line per request.</summary>
    /// <param name="entries">Entries from <see cref="Device.History"/> or <see cref="DroidlineClient.History"/>.</param>
    public static string FormatHistory(IEnumerable<HistoryEntry> entries) =>
        string.Join("\n", entries.Select(e =>
        {
            var stamp = e.Time.ToLocalTime().ToString("HH:mm:ss", CultureInfo.InvariantCulture);
            var parameters = Json.Serialize(e.Params, JsonStyle.Spaced);
            return $"{stamp} {e.Cmd} {parameters} -> {(e.Ok ? "ok" : e.Error)} ({e.Ms} ms)";
        }));

    /// <summary>Save a screenshot, the screen tree and the command log of a phone into folder.</summary>
    /// <remarks>Each part is best effort: a phone that is offline still gets its command log.</remarks>
    /// <param name="device">The phone.</param>
    /// <param name="folder">Created if missing. Gets commands.txt, screen.png and screen.json.</param>
    /// <param name="cancellationToken">Stops waiting for the phone.</param>
    /// <returns>The files written.</returns>
    public static async Task<IReadOnlyList<string>> SaveFailureAsync(Device device, string folder, CancellationToken cancellationToken = default)
    {
        Directory.CreateDirectory(folder);
        var saved = new List<string>();
        var log = Path.Combine(folder, "commands.txt");
        await Files.WriteAllTextAsync(log, FormatHistory(device.History) + "\n", cancellationToken).ConfigureAwait(false);
        saved.Add(log);
        var parts = new (string Name, Func<string, Task> Take)[]
        {
            ("screen.png", path => device.ScreenshotAsync(path, format: "png", cancellationToken: cancellationToken)),
            ("screen.json", path => device.DumpAsync(path, cancellationToken: cancellationToken)),
        };
        foreach (var (name, take) in parts)
        {
            var path = Path.Combine(folder, name);
            try
            {
                await take(path).ConfigureAwait(false);
                saved.Add(path);
            }
            catch (DroidlineException)
            {
                // The phone may be offline or the screen protected; the log above is still useful.
            }
        }
        return saved;
    }
}
