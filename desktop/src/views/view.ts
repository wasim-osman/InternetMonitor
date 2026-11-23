import type { Handlers } from "../dom";

/** A routed screen. `update` is called (rAF-throttled) whenever the model changes. */
export interface View {
  el: HTMLElement;
  update(): void;
  handlers(): Handlers;
  destroy?(): void;
}
