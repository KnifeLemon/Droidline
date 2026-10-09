package dev.droidline.agent.cmd

import org.json.JSONArray
import org.json.JSONObject

class ParamSpec(val name: String, val type: String, val required: Boolean, val default: Any?, val clientOnly: Boolean)

class CmdSpec(
    val name: String,
    val scope: String,
    val params: List<ParamSpec>,
    val requires: List<String>,
    val cutsParam: String?,
    val cutsWhen: Any?,
    val minSdk: Int?,
) {
    /** Parameters the phone receives, in positional order. */
    val wireParams: List<ParamSpec> get() = params.filter { !it.clientOnly }
}

class ErrorSpec(val code: String, val retryable: Boolean, val msgEn: String)

/** The parts of spec/commands.json the phone needs: parameter order and defaults, cuts_network, error templates. */
class CommandSpec(root: JSONObject) {
    val commands: Map<String, CmdSpec>
    val batchOnly: Map<String, CmdSpec>
    val errors: Map<String, ErrorSpec>

    init {
        val common = root.getJSONObject("common_params")
        fun parseParams(arr: JSONArray?): List<ParamSpec> {
            if (arr == null) return emptyList()
            return (0 until arr.length()).map { i ->
                val p = arr.getJSONObject(i)
                val ref = p.optString("ref").ifEmpty { null }
                val def = if (ref != null) common.getJSONObject(ref) else p
                ParamSpec(
                    name = ref ?: p.getString("name"),
                    type = def.optString("type"),
                    required = def.optBoolean("required", false),
                    default = when {
                        p.has("default") -> p.get("default")
                        def.has("default") -> def.get("default")
                        else -> null
                    },
                    clientOnly = def.optBoolean("client_only", false),
                )
            }
        }
        fun parseCmd(o: JSONObject): CmdSpec {
            val cuts = o.optJSONObject("cuts_network")
            val req = o.optJSONArray("requires")
            return CmdSpec(
                name = o.getString("name"),
                scope = o.optString("scope", "device"),
                params = parseParams(o.optJSONArray("params")),
                requires = if (req == null) emptyList() else (0 until req.length()).map { req.getString(it) },
                cutsParam = cuts?.optString("param"),
                cutsWhen = cuts?.opt("when"),
                minSdk = if (o.has("min_sdk")) o.getInt("min_sdk") else null,
            )
        }
        val cmds = root.getJSONArray("commands")
        commands = (0 until cmds.length()).map { parseCmd(cmds.getJSONObject(it)) }.associateBy { it.name }
        val bo = root.optJSONArray("batch_only") ?: JSONArray()
        batchOnly = (0 until bo.length()).map { parseCmd(bo.getJSONObject(it)) }.associateBy { it.name }
        val errs = root.getJSONArray("errors")
        errors = (0 until errs.length()).map { i ->
            val e = errs.getJSONObject(i)
            ErrorSpec(e.getString("code"), e.optBoolean("retryable", false), e.getJSONObject("msg").getString("en"))
        }.associateBy { it.code }
    }

    fun deviceCommand(name: String): CmdSpec? = commands[name]?.takeIf { it.scope == "device" }

    /**
     * Turns a request or batch step into a parameter object: `[cmd, args...]` lists and `args` arrays
     * are mapped onto the parameter order, and missing defaults are filled in.
     */
    fun normalize(step: Any?, inBatch: Boolean = false): JSONObject {
        val obj: JSONObject = when (step) {
            is JSONObject -> JSONObject(step.toString())
            is JSONArray -> {
                if (step.length() == 0) throw IllegalArgumentException("empty step")
                JSONObject().put("cmd", step.getString(0)).put("args", JSONArray().also { a ->
                    for (i in 1 until step.length()) a.put(step.get(i))
                })
            }
            else -> throw IllegalArgumentException("step must be an object or a list")
        }
        val name = obj.optString("cmd")
        val spec = deviceCommand(name) ?: (if (inBatch) batchOnly[name] else null) ?: return obj
        obj.optJSONArray("args")?.let { args ->
            val order = spec.wireParams
            if (args.length() > order.size) throw IllegalArgumentException("too many arguments for $name")
            for (i in 0 until args.length()) if (!obj.has(order[i].name)) obj.put(order[i].name, args.get(i))
            obj.remove("args")
        }
        for (p in spec.wireParams) if (!obj.has(p.name) && p.default != null) obj.put(p.name, p.default)
        return obj
    }

    fun missingRequired(cmd: JSONObject): String? {
        val spec = deviceCommand(cmd.optString("cmd")) ?: batchOnly[cmd.optString("cmd")] ?: return null
        spec.wireParams.firstOrNull { it.required && !cmd.has(it.name) }?.let { return it.name }
        // value is needed only when by names a field; a query object stands alone.
        val takesValue = spec.params.any { it.name == "by" } && spec.params.any { it.name == "value" }
        if (takesValue && cmd.opt("by") is String && !cmd.has("value")) return "value"
        return null
    }

    /** True when running [cmd] cuts the phone's own link; for a batch, when any step does. */
    fun cutsNetwork(cmd: JSONObject): Boolean {
        val name = cmd.optString("cmd")
        if (name == "batch") {
            if (cmd.optBoolean("cuts_network", false)) return true
            val steps = cmd.optJSONArray("steps") ?: return false
            return (0 until steps.length()).any { i ->
                runCatching { cutsNetwork(normalize(steps.get(i), inBatch = true)) }.getOrDefault(false)
            }
        }
        val spec = commands[name] ?: return false
        val param = spec.cutsParam ?: return false
        if (!cmd.has(param)) return false
        return cmd.get(param) == spec.cutsWhen
    }

    fun render(code: String, fields: Map<String, Any?>): String {
        val template = errors[code]?.msgEn ?: return code
        return Regex("\\{([a-z_]+)\\}").replace(template) { m ->
            val v = fields[m.groupValues[1]]
            when (v) {
                null -> ""
                is Collection<*> -> v.joinToString(", ")
                is JSONArray -> (0 until v.length()).joinToString(", ") { v.opt(it).toString() }
                else -> v.toString()
            }
        }
    }

    fun retryable(code: String): Boolean = errors[code]?.retryable ?: false
}
