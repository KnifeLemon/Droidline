import type { By, ClearResult, DeviceCommands, InputResult, Node, Query, TapResult, TouchResult } from "./generated.js";

/**
 * One screen element, as it was when find() or findAll() returned it.
 *
 * The fields are a snapshot. Actions look the element up again by its exact bounds and
 * class, so after it moved or disappeared they reject with NOT_FOUND; call refresh() or
 * find it again. Actions do not wait unless you pass a timeout.
 */
export class Element {
  readonly device: DeviceCommands;
  readonly node: Node;

  constructor(device: DeviceCommands, node: Node) {
    this.device = device;
    this.node = { ...node };
  }

  get text(): string {
    return String(this.node.text ?? "");
  }

  get id(): string {
    return String(this.node.id ?? "");
  }

  get desc(): string {
    return String(this.node.desc ?? "");
  }

  get className(): string {
    return String(this.node.class ?? "");
  }

  get package(): string {
    return String(this.node.package ?? "");
  }

  get bounds(): [number, number, number, number] {
    const b = (this.node.bounds as number[] | undefined) ?? [0, 0, 0, 0];
    return [Number(b[0]), Number(b[1]), Number(b[2]), Number(b[3])];
  }

  get center(): [number, number] {
    const [l, t, r, b] = this.bounds;
    return [Math.floor((l + r) / 2), Math.floor((t + b) / 2)];
  }

  /** The query that finds this element again: its exact bounds and class. */
  query(): Query {
    const q: Query = { bounds: this.bounds };
    if (this.className) q.class = this.className;
    return q;
  }

  click(opts: { timeout?: number } = {}): Promise<TouchResult> {
    return this.device.touch(this.query(), { timeout: opts.timeout ?? 0 });
  }

  longClick(opts: { ms?: number; timeout?: number } = {}): Promise<TouchResult> {
    return this.device.long_touch(this.query(), { ms: opts.ms, timeout: opts.timeout ?? 0 });
  }

  input(text: string, opts: { append?: boolean; timeout?: number } = {}): Promise<InputResult> {
    return this.device.input(this.query(), text, { append: opts.append, timeout: opts.timeout ?? 0 });
  }

  clear(opts: { timeout?: number } = {}): Promise<ClearResult> {
    return this.device.clear(this.query(), { timeout: opts.timeout ?? 0 });
  }

  /** Tap the center point, without looking the element up again. */
  tap(): Promise<TapResult> {
    const [x, y] = this.center;
    return this.device.tap(x, y);
  }

  exists(): Promise<boolean> {
    return this.device.exists(this.query());
  }

  /** The same element with its current fields, for example after its text changed. */
  refresh(opts: { timeout?: number } = {}): Promise<Element> {
    return this.device.find(this.query(), { timeout: opts.timeout ?? 0 });
  }

  /** An element inside this one. */
  find(by: By | Query, value?: string, opts: { nth?: number; timeout?: number } = {}): Promise<Element> {
    return this.device.find(this.within(by, value), opts);
  }

  /** Every element inside this one that matches. */
  findAll(by: By | Query, value?: string, opts: { timeout?: number } = {}): Promise<Element[]> {
    return this.device.findAll(this.within(by, value), opts);
  }

  private within(by: By | Query, value: string | undefined): Query {
    const q: Query = typeof by === "string" ? ({ [by]: value } as Query) : { ...by };
    if (q.inside !== undefined) throw new TypeError("this query already has inside; call device.find() with your own query instead");
    q.inside = this.query();
    return q;
  }

  toString(): string {
    const label = this.text || this.desc || this.id;
    return `Element(${this.className || "?"} ${JSON.stringify(label)} [${this.bounds.join(", ")}])`;
  }
}
