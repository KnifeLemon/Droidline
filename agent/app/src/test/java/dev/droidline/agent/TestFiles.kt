package dev.droidline.agent

import org.json.JSONObject
import java.io.File

object TestFiles {
    fun resource(name: String): String =
        requireNotNull(javaClass.classLoader!!.getResourceAsStream(name)) { "missing test resource $name" }
            .bufferedReader(Charsets.UTF_8).use { it.readText() }

    fun vectors(): JSONObject = JSONObject(resource("test-vectors.json"))

    /** Unit tests run with the app module as working directory; the spec lives two levels up. */
    fun spec(name: String): File = File("../../spec/$name")

    fun asset(name: String): File = File("src/main/assets/$name")
}
