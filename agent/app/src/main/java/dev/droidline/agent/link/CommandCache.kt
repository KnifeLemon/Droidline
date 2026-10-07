package dev.droidline.agent.link

import org.json.JSONObject

/** Last command ids with their outcome, so a command the server resends is never run twice. */
class CommandCache(private val max: Int = 2000) {
    sealed class State {
        data object Running : State()
        /** [body] is the response without n; [n] is the number it was first sent with. */
        class Done(val body: String, val n: Long) : State()
    }

    private val map = object : LinkedHashMap<String, State>(64, 0.75f, false) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, State>?) = size > max
    }

    /** Returns the existing state, or marks [id] as running and returns null. */
    @Synchronized
    fun begin(id: Any?): State? {
        val k = key(id)
        map[k]?.let { return it }
        map[k] = State.Running
        return null
    }

    @Synchronized
    fun finish(id: Any?, body: JSONObject, n: Long) {
        map[key(id)] = State.Done(body.toString(), n)
    }

    @Synchronized
    fun get(id: Any?): State? = map[key(id)]

    @Synchronized
    fun clear() = map.clear()

    val size: Int @Synchronized get() = map.size

    companion object {
        fun key(id: Any?): String = when (id) {
            null, JSONObject.NULL -> "null"
            is String -> "s:$id"
            is Number -> {
                val d = id.toDouble()
                if (d == Math.floor(d) && !d.isInfinite()) "n:${d.toLong()}" else "n:$d"
            }
            else -> "o:$id"
        }
    }
}
