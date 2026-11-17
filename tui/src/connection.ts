// Where the admin URL and token come from. The same rules as the CLI
// (internal/cli/credentials.go): the credentials file `kman init` or `kman
// login` wrote, with KMAN_ADMIN_URL and KMAN_ADMIN_TOKEN overriding it.
import { readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import type { Connection } from './api.js';

export const DEFAULT_ADMIN_URL = 'http://127.0.0.1:8081';

export class ConnectionError extends Error {}

export function defaultConfigDir(env: NodeJS.ProcessEnv = process.env, platform = process.platform): string {
  if (platform === 'win32') {
    if (!env.APPDATA) throw new ConnectionError('%APPDATA% is not set: pass --config-dir');
    return path.join(env.APPDATA, 'kman');
  }
  const xdg = env.XDG_CONFIG_HOME;
  const base = xdg && path.isAbsolute(xdg) ? xdg : path.join(env.HOME || os.homedir(), '.config');
  return path.join(base, 'kman');
}

export async function resolveConnection(
  opts: { configDir?: string; env?: NodeJS.ProcessEnv } = {},
): Promise<Connection> {
  const env = opts.env ?? process.env;
  const file = path.join(opts.configDir ?? defaultConfigDir(env), 'credentials.json');
  let url = '';
  let token = '';
  try {
    const parsed = JSON.parse(await readFile(file, 'utf8')) as { admin_url?: string; token?: string };
    url = parsed.admin_url ?? '';
    token = parsed.token ?? '';
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code;
    if (code !== 'ENOENT') {
      const why = err instanceof SyntaxError ? 'is not valid JSON' : `cannot be read (${code ?? 'error'})`;
      throw new ConnectionError(`${file} ${why}; run kman login again`);
    }
  }
  if (env.KMAN_ADMIN_URL) url = env.KMAN_ADMIN_URL;
  if (env.KMAN_ADMIN_TOKEN?.trim()) token = env.KMAN_ADMIN_TOKEN.trim();
  if (!url) url = DEFAULT_ADMIN_URL;
  if (!token) {
    throw new ConnectionError('no admin token: run kman login --admin-url <url>, or set KMAN_ADMIN_TOKEN');
  }
  return { url, token };
}
