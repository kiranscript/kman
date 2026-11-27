// The kman command that does what a dialog is about to do, so an operator can see it, and
// run it elsewhere: the TUI does nothing the CLI cannot (ADR 0004).
import type { PickKind, Resolution } from './api.js';

/** A word as a shell takes it: quoted unless it is plainly safe. */
const word = (s: string) => (/^[A-Za-z0-9_][A-Za-z0-9_.:/@-]*$/.test(s) ? s : `'${s.replaceAll("'", `'\\''`)}'`);

const forIncident = (id: number | undefined) => (id === undefined ? '' : ` --incident ${id}`);

/** How a resolution is written for a person, and for --as. */
export const resolutionWord = (r: string) => r.replaceAll('_', ' ');

export const cli = {
  disable: (key: string, incident?: number) => `kman key disable ${word(key)}${forIncident(incident)}`,
  enable: (key: string) => `kman key enable ${word(key)}`,
  rotate: (key: string, incident?: number) => `kman key rotate ${word(key)}${forIncident(incident)}`,
  revoke: (key: string, incident?: number) => `kman key revoke ${word(key)}${forIncident(incident)}`,
  resolve: (id: number, as: Resolution) => `kman incident resolve ${id} --as ${as.replaceAll('_', '-')}`,
  thisWasMe: (id: number, kinds: PickKind[], everyKey: boolean) =>
    `kman incident this-was-me ${id}${kinds.length ? ` --add-known-sources --kinds ${kinds.join(',')}${everyKey ? ' --install-wide' : ''}` : ''}`,
  resetCanary: (id: string) => `kman canary reset ${word(id)}`,
};
