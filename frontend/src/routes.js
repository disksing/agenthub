export const BEEPER_PATH = "/beeper";

export function isBeeperPath(pathname) {
  return String(pathname || "").replace(/\/+$/, "") === BEEPER_PATH;
}
