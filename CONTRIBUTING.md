# Contributing

## Adding or changing a command

Every command lives in [`spec/commands.json`](spec/commands.json). Change it there first, then:

1. `node scripts/gen.mjs` regenerates the Python and Node methods.
2. Implement the command on the phone in `agent/` (and in the simulator in `server/internal/fakeagent/ui.go`, so SDK tests can use it).
3. If the server must validate something the JSON cannot express, add it to `special()` in `spec/normalize.go` with a test.
4. Summaries, notes and error messages need English, Korean and Simplified Chinese.

The CLI, the MCP tool list and the docs site read the same file, so they pick the change up without code.

## Settings recipes on your phone

Force stop, clearing app data and the Wi-Fi, mobile data and airplane mode switches are not commands. Settings differs by manufacturer, Android version and language, so they are [recipes in the docs](https://droidline.dev/docs/recipes/#settings-force-stop-clear-data-network-switches) built from `intent`, `touch` and `batch` with `cuts_network`. The recipes were checked on Samsung phones in Korean. If a recipe needs different labels or steps on your phone, open an issue with your phone model, Android version, system language and a `dump` of the screen where it stopped.

## Before a pull request

```bash
go vet ./spec/ ./server/... && go test ./spec/ ./server/...
node scripts/gen.mjs --check
cd sdk/python && python -m pytest
cd sdk/node && npm test
cd agent && ./gradlew testDebugUnitTest
```

Keep comments short and only for what the code does not already say.
