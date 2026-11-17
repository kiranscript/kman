import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { ConnectionError, defaultConfigDir, resolveConnection } from '../src/connection.js';

async function dirWith(creds?: string) {
  const dir = await mkdtemp(path.join(tmpdir(), 'kman-cfg-'));
  if (creds !== undefined) await writeFile(path.join(dir, 'credentials.json'), creds);
  return dir;
}

describe('resolveConnection', () => {
  it('reads the credentials file the CLI wrote', async () => {
    const configDir = await dirWith('{"admin_url":"http://127.0.0.1:9","token":"kma_file"}');
    expect(await resolveConnection({ configDir, env: {} })).toEqual({ url: 'http://127.0.0.1:9', token: 'kma_file' });
  });

  it('lets the environment override the file', async () => {
    const configDir = await dirWith('{"admin_url":"http://127.0.0.1:9","token":"kma_file"}');
    const env = { KMAN_ADMIN_URL: 'unix:/run/kman.sock', KMAN_ADMIN_TOKEN: '  kma_env ' };
    expect(await resolveConnection({ configDir, env })).toEqual({ url: 'unix:/run/kman.sock', token: 'kma_env' });
  });

  it('works from the environment alone, with the default address', async () => {
    const configDir = await dirWith();
    expect(await resolveConnection({ configDir, env: { KMAN_ADMIN_TOKEN: 'kma_env' } })).toEqual({
      url: 'http://127.0.0.1:8081',
      token: 'kma_env',
    });
  });

  it('says what to do with no token', async () => {
    const configDir = await dirWith();
    await expect(resolveConnection({ configDir, env: {} })).rejects.toThrow(/kman login/);
  });

  it('names the file when it is not JSON, and never quotes its contents', async () => {
    const configDir = await dirWith('kma_secret not json');
    const err = await resolveConnection({ configDir, env: {} }).catch((e) => e);
    expect(err).toBeInstanceOf(ConnectionError);
    expect(err.message).toContain('credentials.json');
    expect(err.message).not.toContain('kma_secret');
  });
});

describe('defaultConfigDir', () => {
  it('follows the CLI: XDG, then ~/.config, and %APPDATA% on Windows', () => {
    expect(defaultConfigDir({ XDG_CONFIG_HOME: '/x', HOME: '/h' }, 'linux')).toBe('/x/kman');
    expect(defaultConfigDir({ XDG_CONFIG_HOME: 'relative', HOME: '/h' }, 'linux')).toBe('/h/.config/kman');
    expect(defaultConfigDir({ HOME: '/h' }, 'linux')).toBe('/h/.config/kman');
    expect(defaultConfigDir({ APPDATA: 'C:\\A' }, 'win32')).toBe(path.join('C:\\A', 'kman'));
  });
});
