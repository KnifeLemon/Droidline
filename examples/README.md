# Examples

`demo.py` and `demo.mjs` drive the demo app inside `droidline-fakephone`, so they run without a phone:

```bash
droidline serve                 # terminal 1
droidline-fakephone             # terminal 2, prints a 6-digit code
droidline pair <code>           # terminal 3
python demo.py
npm install && node demo.mjs
```

`scripts/integration.sh` does all of that in one go and is what CI runs.
