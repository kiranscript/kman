#!/usr/bin/env node
// kman-tui: a terminal client for a running kman. It reads the same credentials
// the kman CLI uses, and every change it makes is a call to the admin API.
import { render } from 'ink';
import { createClient } from './api.js';
import { App } from './App.js';
import { ConnectionError, resolveConnection } from './connection.js';

const USAGE = `usage: kman-tui [--config-dir dir]

A terminal client for a running kman. It reads the admin URL and token the way
the kman CLI does: the credentials file in the config directory, overridden by
KMAN_ADMIN_URL and KMAN_ADMIN_TOKEN. It talks only to that admin address.
  --config-dir dir   the config directory holding the credentials file
                     (default ~/.config/kman, or %APPDATA%\\kman on Windows)
  --help             print this
NO_COLOR turns colour off (bold and inverse stay); nothing relies on colour alone.
`;

async function main(argv: string[]): Promise<number> {
  let configDir: string | undefined;
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]!;
    if (a === '--help' || a === '-h') {
      process.stdout.write(USAGE);
      return 0;
    }
    if (a === '--config-dir' || a === '-config-dir') {
      configDir = argv[++i];
      if (!configDir) return fail('--config-dir needs a directory');
    } else if (a.startsWith('--config-dir=')) {
      configDir = a.slice('--config-dir='.length);
    } else {
      return fail(`unknown argument ${a}`);
    }
  }

  if (!process.stdin.isTTY || !process.stdout.isTTY) {
    return fail('kman-tui needs a terminal. For scripts, use the kman CLI with --json.');
  }

  let conn;
  try {
    conn = await resolveConnection({ configDir });
  } catch (err) {
    if (err instanceof ConnectionError) return fail(err.message);
    throw err;
  }

  const app = render(<App client={createClient(conn)} url={conn.url} configDir={configDir} tokenFromEnv={Boolean(process.env.KMAN_ADMIN_TOKEN?.trim())} />, { alternateScreen: true });
  await app.waitUntilExit();
  return 0;
}

function fail(message: string): number {
  process.stderr.write(`kman-tui: ${message}\n`);
  return 1;
}

process.exitCode = await main(process.argv.slice(2));
