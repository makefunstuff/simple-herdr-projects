// Elements are only props here: each call is recorded, `when` runs its branch, and everything
// returns the same element so a chain builds. Tests read el.calls and invoke stored handlers.

export class View {}

export function element() {
  const el = { calls: {} };
  const proxy = new Proxy(el, {
    get(target, key) {
      if (key === "calls") return target.calls;
      return (...args) => {
        if (key === "when") {
          if (args[0]) args[1](proxy);
          return proxy;
        }
        (target.calls[key] ??= []).push(args);
        return proxy;
      };
    },
  });
  return proxy;
}

export const div = element;
export const svg = element;
export const image = element;
