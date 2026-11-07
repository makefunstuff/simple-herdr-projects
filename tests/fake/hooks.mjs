// Run with --import: Herdr's modules resolve to the fakes beside this file, so main.js loads
// without the app, and board-main.mjs shares the same fake instances.
import { registerHooks } from "node:module";

const fakes = {
  herdr: "./herdr.mjs",
  "gpui-kit": "./gpui-kit.mjs",
  "gpui-base": "./gpui-base.mjs",
};

registerHooks({
  resolve(specifier, context, nextResolve) {
    const fake = fakes[specifier];
    if (fake) return { url: new URL(fake, import.meta.url).href, shortCircuit: true };
    return nextResolve(specifier, context);
  },
});
