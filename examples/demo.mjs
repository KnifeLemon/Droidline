// Runs against droidline-fakephone's demo app. Also a CI smoke test.
import { connect, DroidlineError } from "droidline";
import assert from "node:assert/strict";

const d = await connect();
await d.home();
await d.launch("dev.droidline.demo");
if (await d.exists("text", "Close ad")) await d.touch("text", "Close ad");
await d.input("id", "email", "node");
await d.touchById("login");
assert.equal(await d.get_text("id", "greeting"), "Welcome, node");
assert.equal(await d.which([["text", "Log in"], ["id", "main_tab"]], { timeout: 5 }), 1);

await assert.rejects(d.touch("text", "Does not exist", { timeout: 0.5 }), (e) => e instanceof DroidlineError && e.code === "NOT_FOUND");
const png = await d.screenshot({ format: "png", scale: 0.5 });
assert.ok(png.length > 100);
console.log("node demo ok");
