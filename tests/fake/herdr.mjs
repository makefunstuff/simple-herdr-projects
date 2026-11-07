// Stubs for the modules a mod imports, so tests can load main.js away from Herdr.
// board-main.mjs imports this very module (as "herdr") to set replies and read the calls.

export const state = {
  props: {},      // props() for the copy under test
  spaces: [],     // spaces() the rows see
  machines: [],   // `hc machine ls --json`
  exec: async () => ({ code: 0, stdout: "[]", stderr: "" }), // exec() answers
};

export const calls = { exec: [], hc: [], runs: [], notices: [], overlays: [], closed: [] };

export const props = () => state.props;
export const spaces = () => state.spaces;
export const machines = () => [];
export const shared = () => null;
export const theme = () => new Proxy({}, { get: () => "#000000" });

export async function exec(command, args, options) {
  calls.exec.push({ command, args, on: options?.on ?? null });
  return state.exec(command, args, options);
}

export async function hc(args) {
  calls.hc.push(args);
  return { code: 0, stdout: state.machines, stderr: "" };
}

export function run(name, args) {
  calls.runs.push({ name, args });
  return null;
}

export function notify(note) {
  calls.notices.push(note);
  return "toast";
}

export function openOverlay(spec) {
  calls.overlays.push(spec);
  return "ov1";
}

export function closeOverlay(id) {
  calls.closed.push(id);
  return null;
}
