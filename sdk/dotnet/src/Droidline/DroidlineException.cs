using System;

namespace Droidline;

/// <summary>
/// Every failure the SDK reports. <see cref="Code"/> is an error code from spec/commands.json (see
/// <see cref="ErrorCodes"/>) or a client-side code such as SERVER_NOT_RUNNING; <see cref="Fields"/> holds the extra
/// fields the server sent, like screen or candidates. Known codes are thrown as subclasses such as
/// <see cref="NotFoundException"/>.
/// </summary>
public class DroidlineException : Exception
{
    /// <summary>Creates an exception with a code and the server's extra fields.</summary>
    /// <param name="message">The message, in the server's language for server errors.</param>
    /// <param name="code">The error code, such as NOT_FOUND.</param>
    /// <param name="retryable">Whether the call is safe to retry.</param>
    /// <param name="fields">The extra fields the server sent.</param>
    /// <param name="http">The HTTP status the server uses for this code, when it is known.</param>
    public DroidlineException(string message, string code, bool retryable = false, DroidlineObject? fields = null, int? http = null)
        : base(message)
    {
        Code = code;
        Retryable = retryable;
        Fields = fields ?? new DroidlineObject();
        Http = http;
    }

    /// <summary>The error code, such as NOT_FOUND or CONNECTION_LOST.</summary>
    public string Code { get; }

    /// <summary>Whether the call is safe to retry, as the server said or as the code's default.</summary>
    public bool Retryable { get; }

    /// <summary>The extra fields the server sent, such as screen for NOT_FOUND. Empty for client-side errors.</summary>
    public DroidlineObject Fields { get; }

    /// <summary>The HTTP status the server's local HTTP API uses for this code, or null for unknown and client-side codes.</summary>
    public int? Http { get; }
}

/// <summary>Nothing is listening on the client port. Start the server with <c>droidline serve</c>.</summary>
public sealed class ServerNotRunningException : DroidlineException
{
    /// <summary>Creates the exception.</summary>
    /// <param name="message">What was tried, and how to start the server.</param>
    public ServerNotRunningException(string message)
        : base(message, ErrorCodes.ServerNotRunning)
    {
    }
}

/// <summary>The server connection dropped while a call was in flight. The command may or may not have run.</summary>
public sealed class ConnectionLostException : DroidlineException
{
    /// <summary>Creates the exception.</summary>
    /// <param name="message">What happened to the call.</param>
    public ConnectionLostException(string message)
        : base(message, ErrorCodes.ConnectionLost)
    {
    }
}

public static partial class ErrorCodes
{
    /// <summary>Raised by the SDK: nothing is listening on the client port.</summary>
    public const string ServerNotRunning = "SERVER_NOT_RUNNING";

    /// <summary>Raised by the SDK: the connection dropped before the reply arrived.</summary>
    public const string ConnectionLost = "CONNECTION_LOST";
}
