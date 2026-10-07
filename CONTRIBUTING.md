# Contributing

## Adding or changing a command

Every command lives in [`spec/commands.json`](spec/commands.json). Change it there first, then:

1. `node scripts/gen.mjs` regenerates the Python and Node methods.
2. Implement the command on the phone in `agent/` (and in the simulator in `server/internal/fakeagent/ui.go`, so SDK tests can use it).
3. If the server must validate something the JSON cannot express, add it to `special()` in `spec/normalize.go` with a test.
4. Summaries, notes and error messages need English, Korean and Simplified Chinese.

The CLI, the MCP tool list and the docs site read the same file, so they pick the change up without code.

## Settings macros on your phone

`kill`, `clear_data`, `data`, `wifi` and `airplane` press buttons in Settings, and labels differ by manufacturer and language. If one fails on your phone, open an issue with the `MACRO_FAILED` response (it names the step and screen), your phone model, Android version and system language. Adding the label to `agent/app/src/main/assets/macro_labels.json` is usually the whole fix.

## Before a pull request

```bash
go vet ./spec/ ./server/... && go test ./spec/ ./server/...
node scripts/gen.mjs --check
cd sdk/python && python -m pytest
cd sdk/node && npm test
cd agent && ./gradlew testDebugUnitTest
```

Keep comments short and only for what the code does not already say.
