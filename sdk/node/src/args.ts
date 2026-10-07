export type Params = Record<string, unknown>;

function isOptions(value: unknown): value is Params {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

/**
 * Maps a generated method's arguments onto named params: the required ones, then optional
 * ones by position, then a trailing plain object read as options. Undefined values are dropped
 * so the server fills its own defaults.
 */
export function collect(
  cmd: string,
  args: readonly unknown[],
  required: readonly string[],
  positional: readonly string[],
): Params {
  const params: Params = {};
  required.forEach((name, i) => {
    if (args[i] !== undefined) params[name] = args[i];
  });
  const rest = args.slice(required.length);
  const options = rest.length > 0 && isOptions(rest[rest.length - 1]) ? (rest.pop() as Params) : {};
  if (rest.length > positional.length) {
    throw new TypeError(`${cmd}() takes at most ${required.length + positional.length} arguments before the options object`);
  }
  positional.forEach((name, i) => {
    if (rest[i] !== undefined) params[name] = rest[i];
  });
  for (const [name, value] of Object.entries(options)) {
    if (value !== undefined) params[name] = value;
  }
  return params;
}
