import type { Relay } from "./relay";

export interface Env {
  RELAY: DurableObjectNamespace<Relay>;
  // Set with `npx wrangler secret put RELAY_TOKEN`; missing until the owner sets it.
  RELAY_TOKEN?: string;
}
