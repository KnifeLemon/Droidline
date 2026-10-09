# Examples

`demo.py`, `demo.mjs` and `dotnet/Program.cs` drive the demo app inside `droidline-fakephone`, so they run without a phone:

```bash
droidline serve                 # terminal 1
droidline-fakephone             # terminal 2, prints a 6-digit code
droidline pair <code>           # terminal 3
python demo.py
npm install && node demo.mjs
dotnet run --project dotnet
```

`scripts/integration.sh` does all of that for the Python and Node.js demos in one go and is what CI runs.
