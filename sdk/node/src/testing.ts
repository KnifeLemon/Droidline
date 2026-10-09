import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { Device, HistoryEntry } from "./client.js";

/** The command log as text, one line per request. */
export function formatHistory(entries: readonly HistoryEntry[]): string {
  return entries
    .map((e) => {
      const stamp = new Date(e.time).toTimeString().slice(0, 8);
      return `${stamp} ${e.cmd} ${JSON.stringify(e.params)} -> ${e.ok ? "ok" : e.error} (${e.ms} ms)`;
    })
    .join("\n");
}

/**
 * Save a screenshot, the screen tree and the command log of a phone into folder, for a failed
 * test. Each part is best effort: a phone that is offline still gets its command log.
 * Resolves with the files written.
 *
 * @example
 * afterEach(async (ctx) => {
 *   if (ctx.task.result?.state === "fail") await saveFailure(d, `artifacts/${ctx.task.name}`);
 * });
 */
export async function saveFailure(device: Device, folder: string): Promise<string[]> {
  await mkdir(folder, { recursive: true });
  const saved: string[] = [];
  const log = join(folder, "commands.txt");
  await writeFile(log, formatHistory(device.history) + "\n", "utf8");
  saved.push(log);
  const parts: Array<[string, (path: string) => Promise<unknown>]> = [
    ["screen.png", (p) => device.screenshot(p, { format: "png" })],
    ["screen.json", (p) => device.dump(p)],
  ];
  for (const [name, take] of parts) {
    const path = join(folder, name);
    try {
      await take(path);
      saved.push(path);
    } catch {
      // The phone may be offline or the screen protected; the log above is still useful.
    }
  }
  return saved;
}
