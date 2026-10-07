package dev.droidline.agent

import android.app.Application
import dev.droidline.agent.service.AgentService

class DroidlineApp : Application() {
    override fun onCreate() {
        super.onCreate()
        Agent.init(this)
        AgentService.createChannels(this)
        if (Agent.isPaired) AgentService.start(this)
    }
}
