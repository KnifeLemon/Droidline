package dev.droidline.agent.cmd

/** A command failure with a code from commands.json and the fields its message template uses. */
class CmdError(val code: String, val fields: Map<String, Any?> = emptyMap()) : Exception(code) {
    companion object {
        fun badArgs(cmd: String, reason: String) = CmdError("BAD_ARGS", mapOf("cmd" to cmd, "reason" to reason))
        fun noPermission(permission: String) = CmdError("NO_PERMISSION", mapOf("permission" to permission))
        fun internal(reason: String) = CmdError("INTERNAL", mapOf("reason" to reason))
    }
}
