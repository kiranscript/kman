// The incident view: opening it, what it shows, each answer it can send and the safeguard
// each one has.
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api.js';
import { App } from '../src/App.js';
import { detection, fakeClient, incident, key, leak, NOW, tick, until } from './fakes.js';
import type { Fake } from './fakes.js';
import { render } from './render.js';

const SMALL = { columns: 80, rows: 24 };
const WIDE = { columns: 140, rows: 40 };
const ENTER = '\r';
const ESC = '\u001b';

/** prod-api with an open incident, and a canary that has fired. */
function leaked(): Fake {
  const client = fakeClient();
  client.keys.push(key({ id: 'k_prod', name: 'prod-api' }));
  client.incidents = [
    incident({ id: 12, key_id: 'k_prod', key_name: 'prod-api', rules: ['new_country', 'new_asn', 'new_client'], detection_count: 3, last_fired_at: new Date(NOW - 4 * 60_000).toISOString() }),
    incident({ id: 13, key_id: 'k_wiki', key_name: 'wiki-canary', severity: 'critical', rules: ['canary'] }),
  ];
  client.recorded = {
    12: leak(),
    13: { detections: [detection(9, 'canary', { country: 'RO', client: 'python-requests/2', detail: 'planted: the .env of the wiki' })] },
  };
  return client;
}

function mount(client = leaked(), size = SMALL, actionGuardMs = 0, intervalMs = 60_000) {
  const element = <App client={client} url="http://127.0.0.1:8081" size={size} intervalMs={intervalMs} secretGuardMs={0} actionGuardMs={actionGuardMs} />;
  const app = render(element, size.columns);
  const screen = () => app.lastFrame() ?? '';
  const settle = async () => {
    await tick(40);
    for (let same = 0, last = screen(); same < 2; ) {
      await tick(25);
      const now = screen();
      same = now === last ? same + 1 : 0;
      last = now;
    }
  };
  const press = async (...keys: string[]) => {
    for (const k of keys) {
      app.stdin.write(k);
      await settle();
    }
  };
  return { ...app, press, settle, screen, client };
}

type Mounted = ReturnType<typeof mount>;

const loaded = (m: Mounted) => until(() => m.screen().includes('live'), 'the first load');

/** Opens prod-api's incident from the first row of Status, or the canary's from the second. */
async function opened(m: Mounted, which: 12 | 13 = 12) {
  await loaded(m);
  // The canary's is critical, so it is listed first.
  if (which === 12) await m.press('j');
  await m.press(ENTER);
  await until(() => m.screen().includes('TIMELINE') || m.screen().includes('WHAT FIRED'), `incident ${which}`);
  await m.settle();
  expect(m.screen()).toContain(`Incident #${which} ·`);
}

const changes = (c: Fake) => c.calls.filter((x) => !x.startsWith('clients'));

/** Every audit entry the changes made, as "action incident". */
const audited = (c: Fake) => c.audited.map((e) => `${e.action} ${e.incident_id ?? '-'}`);

describe('opening an incident', () => {
  it('opens from a key row in Status, shows what fired, and acknowledges nothing', async () => {
    const m = mount(leaked(), WIDE);
    await opened(m);
    const s = m.screen();
    // The app reads the real clock, so how long ago the fixtures were grows.
    expect(s).toMatch(/! HIGH · OPEN · opened \d+d ago · last fired \d+d ago · 3 detections/);
    expect(s).toMatch(/new country +NL +usually US \(9100 requests\)/);
    expect(s).toMatch(/new ASN +AS60729 Stiftung/);
    expect(s).toMatch(/new client +curl\/8 +usually openai-python\/1/);
    expect(s).toContain('All refused: address_not_allowed');
    expect(s).toContain('WHERE FROM');
    expect(s).toContain('NL · AS60729 Stiftung Erneuerbare Freiheit · curl/8');
    expect(s).toContain('Addresses are withheld');
    expect(s).toContain('opened by system:detect');
    for (const label of ['a acknowledge', 'm this was me…', 'R resolve…', 'd disable…', 'o rotate…', 'x revoke…']) expect(s).toContain(label);
    expect(s).toContain('Esc back');
    expect(changes(m.client)).toEqual([]);
    expect(m.client.incidents[0]!.state).toBe('open');
    await m.press(ESC);
    expect(m.screen()).toContain('NEEDS ATTENTION');
    expect(m.screen()).not.toContain('WHAT FIRED');
    m.unmount();
  });

  it('opens from a canary row, offering only acknowledge, resolve and reset', async () => {
    const m = mount();
    await opened(m, 13);
    const s = m.screen();
    expect(s).toContain('Incident #13 · wiki-canary');
    expect(s).toContain('planted: the .env of the wiki');
    for (const label of ['a acknowledge', 'R resolve…', 'z reset canary…']) expect(s).toContain(label);
    for (const label of ['this was me', 'disable', 'rotate', 'revoke']) expect(s).not.toContain(label);
    expect(s).toContain('A fired canary stays silent until it is reset.');
    for (const k of ['m', 'd', 'o', 'x']) await m.press(k);
    expect(m.screen()).toContain('A canary offers only acknowledge, resolve and reset.');
    expect(changes(m.client)).toEqual([]);
    m.unmount();
  });

  it('opens from a key row in Keys, where J and K still scroll the detail', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    // checkout, ci-evals, prod-api, by name.
    await m.press('j', 'j');
    expect(m.screen()).toContain('Key: prod-api');
    expect(m.screen()).toContain('Enter incident');
    await m.press('J');
    expect(m.screen()).toContain('Key: prod-api');
    expect(m.screen()).not.toContain('WHAT FIRED');
    await m.press(ENTER);
    expect(m.screen()).toContain('Incident #12 · prod-api');
    await m.press(ESC);
    expect(m.screen()).toContain('Key: prod-api');
    m.unmount();
  });

  it('leaves Enter on a key with no incident to hand the detail the keyboard', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2', ENTER);
    expect(m.screen()).not.toContain('Incident #');
    expect(m.screen()).toContain('The whole detail is already showing.');
    m.unmount();
  });

  it('fits 80x24, scrolling what is above the actions', async () => {
    const client = leaked();
    client.recorded[12]!.actions = Array.from({ length: 30 }, (_, n) => ({ id: n + 1, time: new Date(NOW).toISOString(), actor: `operator-${n}`, action: 'incident.escalate', target_kind: 'incident', target_id: '12' }));
    const m = mount(client);
    await opened(m);
    const rows = () => m.screen().split('\n');
    expect(rows().length).toBeLessThanOrEqual(24);
    for (const l of rows()) expect([...l].length).toBeLessThanOrEqual(80);
    expect(m.screen()).toContain('x revoke…');
    expect(m.screen()).toContain('↓ more · Esc back');
    await m.press('G');
    expect(m.screen()).toContain('escalated by operator-29');
    expect(m.screen()).toContain('x revoke…');
    await m.press('g');
    expect(m.screen()).toContain('WHAT FIRED');
    m.unmount();
  });

  it('says why it could not load, and offers no action', async () => {
    const client = leaked();
    client.incident = async () => {
      throw new ApiError('internal', 'database is locked', 500);
    };
    const m = mount(client);
    await loaded(m);
    await m.press('j', ENTER);
    expect(m.screen()).toContain('Could not load it: database is locked');
    expect(m.screen()).not.toContain('x revoke');
    await m.press('x', 'a');
    expect(changes(client)).toEqual([]);
    m.unmount();
  });

  it('lists the incident keys in the help', async () => {
    const m = mount();
    await opened(m);
    await m.press('?');
    expect(m.screen()).toContain('INCIDENT');
    expect(m.screen()).toContain('this was me');
    expect(m.screen()).toContain('MARKS');
    expect(m.screen().split('\n').length).toBeLessThanOrEqual(24);
    await m.press(ESC);
    expect(m.screen()).toContain('Incident #12 · prod-api');
    m.unmount();
  });
});

describe('answering an incident', () => {
  it('acknowledges with a, leaving it unresolved', async () => {
    const m = mount();
    await opened(m);
    await m.press('a');
    expect(changes(m.client)).toEqual(['acknowledge 12']);
    expect(audited(m.client)).toEqual(['incident.acknowledge 12']);
    expect(m.screen()).toContain('Acknowledged incident #12');
    expect(m.screen()).toContain('HIGH · ACKNOWLEDGED');
    expect(m.screen()).toContain('Acknowledged by owner');
    expect(m.screen()).not.toContain('a acknowledge');
    await m.press('a');
    expect(m.screen()).toContain('already acknowledged');
    expect(changes(m.client)).toHaveLength(1);
    m.unmount();
  });

  it('this was me: with nothing ticked it resolves and adds no known source', async () => {
    const m = mount();
    await opened(m);
    await m.press('m');
    const s = m.screen();
    expect(s).toContain('This was you? Incident #12');
    expect(s).toContain('[ ] country');
    expect(s).toContain('[ ] ASN');
    expect(s).toContain('[ ] client');
    expect(s).toContain('unavailable: withheld');
    expect(s).toContain('For this key only');
    expect(s).toContain('kman incident this-was-me 12');
    expect(s).not.toContain('--add-known-sources');
    expect(s.split('\n').length).toBeLessThanOrEqual(24);
    await m.press(ENTER);
    expect(changes(m.client)).toEqual(['this-was-me 12 {"add_known_sources":false}']);
    expect(audited(m.client)).toEqual(['incident.this_was_me 12']);
    expect(m.screen()).toContain('#12 resolved: false positive.');
    expect(m.screen()).toContain('Resolved by owner as false positive');
    m.unmount();
  });

  it('this was me: adds the ticked kinds, for every key if chosen, and never the withheld network', async () => {
    const m = mount();
    await opened(m);
    await m.press('m', ' ', 'j', 'j', ' ');
    expect(m.screen()).toContain('[x] country');
    expect(m.screen()).toContain('[x] client');
    // The network row cannot be ticked.
    await m.press('j', ' ', 'j', ' ');
    expect(m.screen()).toContain('For every key');
    // The command wraps and is never cut.
    expect(m.screen()).toContain('--add-known-sources --kinds');
    expect(m.screen()).toContain('country,client --install-wide');
    await m.press(ENTER);
    expect(changes(m.client)).toEqual(['this-was-me 12 {"add_known_sources":true,"kinds":["country","client"],"install_wide":true}']);
    expect(m.screen()).toContain('Added 2 known sources for every key');
    m.unmount();
  });

  it('this was me: unticks and asks again when the incident has seen more since', async () => {
    const m = mount();
    await opened(m);
    await m.press('m', ' ');
    m.client.recorded[12]!.known_source_candidates!.push({ kind: 'country', value: 'RU' });
    await m.press(ENTER);
    expect(changes(m.client)).toEqual([]);
    expect(m.screen()).toContain('What it saw has changed');
    expect(m.screen()).toContain('[ ] country');
    expect(m.screen()).toContain('NL, RU');
    await m.press(' ', ENTER);
    expect(changes(m.client)).toEqual(['this-was-me 12 {"add_known_sources":true,"kinds":["country"]}']);
    m.unmount();
  });

  it('resolves only after a choice, which has no default, and a y', async () => {
    const m = mount();
    await opened(m);
    await m.press('R');
    expect(m.screen()).toContain('Was it real?');
    await m.press(ENTER, 'y');
    expect(m.screen()).toContain('Was it real?');
    await m.press('c');
    expect(m.screen()).toContain('Resolve it as confirmed? y/N');
    expect(m.screen()).toContain('kman incident resolve 12 --as confirmed');
    // N is the default: Enter says no.
    await m.press(ENTER);
    expect(changes(m.client)).toEqual([]);
    expect(m.screen()).toContain('WHAT FIRED');
    await m.press('R', 'f');
    expect(m.screen()).toContain('kman incident resolve 12 --as false-positive');
    await m.press('y');
    expect(changes(m.client)).toEqual(['resolve 12 false_positive']);
    expect(audited(m.client)).toEqual(['incident.resolve 12']);
    // The view stays open, saying who resolved it, with nothing left to do.
    expect(m.screen()).toContain('Resolved by owner as false positive');
    expect(m.screen()).toContain('nothing left to answer');
    await m.press('x');
    expect(m.screen()).not.toContain('Revoke prod-api?');
    m.unmount();
  });

  it('disables after a y, for the incident, which stays open, and enables again', async () => {
    const m = mount();
    await opened(m);
    await m.press('d');
    expect(m.screen()).toContain('Disable this key?');
    expect(m.screen()).toContain('kman key disable prod-api --incident 12');
    await m.press(ENTER);
    expect(changes(m.client)).toEqual([]);
    await m.press('d', 'y');
    expect(changes(m.client)).toEqual(['disable k_prod 12']);
    expect(audited(m.client)).toEqual(['key.disable 12']);
    expect(m.screen()).toContain('Incident #12 stays open.');
    expect(m.screen()).toContain('HIGH · OPEN');
    expect(m.screen()).toContain('e enable…');
    await m.press('e');
    expect(m.screen()).toContain('kman key enable prod-api');
    await m.press('y');
    expect(changes(m.client)).toEqual(['disable k_prod 12', 'enable k_prod']);
    expect(m.screen()).toContain('d disable…');
    m.unmount();
  });

  it('rotates after the name is typed, shows the new secret once, and resolves it', async () => {
    const m = mount();
    await opened(m);
    await m.press('o');
    expect(m.screen()).toContain('Rotate prod-api?');
    expect(m.screen()).toContain('kman key rotate prod-api --incident 12');
    expect(m.screen()).toContain('Incident #12 is resolved as confirmed.');
    await m.press('prod', ENTER);
    expect(changes(m.client)).toEqual([]);
    await m.press('-api', ENTER);
    expect(changes(m.client)).toEqual(['rotate k_prod 12']);
    expect(audited(m.client)).toEqual(['key.rotate 12', 'incident.resolve 12']);
    expect(m.screen()).toContain('Key rotated: prod-api');
    expect(m.screen()).toContain('kmn_rotatedrotatedrotated');
    await m.press(ESC);
    expect(m.screen()).toContain('kmn_rotatedrotatedrotated');
    await m.press(ENTER);
    expect(m.screen()).not.toContain('kmn_rotated');
    expect(m.screen()).toContain('Resolved by owner as confirmed');
    m.unmount();
  });

  it('revokes after the name is typed, for the incident', async () => {
    const m = mount();
    await opened(m);
    await m.press('x');
    expect(m.screen()).toContain('kman key revoke prod-api --incident 12');
    await m.press('prod-api', ENTER);
    expect(changes(m.client)).toEqual(['revoke k_prod 12']);
    expect(audited(m.client)).toEqual(['key.revoke 12', 'incident.resolve 12']);
    expect(m.screen()).toContain('Resolved by owner as confirmed');
    expect(m.screen()).toContain('Revoked prod-api.');
    m.unmount();
  });

  it('arms a canary again after a y, before or after resolving it', async () => {
    const m = mount();
    await opened(m, 13);
    await m.press('z');
    expect(m.screen()).toContain('Arm this canary again?');
    expect(m.screen()).toContain('kman canary reset k_wiki');
    await m.press(ENTER);
    expect(changes(m.client)).toEqual([]);
    await m.press('z', 'y');
    expect(changes(m.client)).toEqual(['reset k_wiki']);
    expect(m.screen()).toContain('wiki-canary is armed');
    await m.press('R', 'c', 'y');
    expect(m.screen()).toContain('Resolved by owner as confirmed');
    expect(m.screen()).toContain('z reset canary…');
    await m.press('z', 'y');
    expect(changes(m.client)).toEqual(['reset k_wiki', 'resolve 13 confirmed', 'reset k_wiki']);
    m.unmount();
  });

  it('keeps d and x in the Keys panel unlinked from the incident', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2', 'j', 'j', 'd');
    expect(m.screen()).toContain('kman key disable prod-api');
    expect(m.screen()).not.toContain('--incident');
    await m.press('y', 'x', 'prod-api', ENTER);
    expect(changes(m.client)).toEqual(['disable k_prod', 'revoke k_prod']);
    expect(audited(m.client)).toEqual(['key.disable -', 'key.revoke -']);
    m.unmount();
  });
});

describe('at the smallest size', () => {
  it('keeps every dialog and its command on screen with long values and an error', async () => {
    const client = leaked();
    client.keys.at(-1)!.name = 'production-billing-reconciliation-service-eu-west';
    client.incidents[0]!.key_name = client.keys.at(-1)!.name;
    const many = (kind: 'country' | 'asn' | 'client' | 'cidr', n: number, value: (i: number) => string) => Array.from({ length: n }, (_, i) => ({ kind, value: value(i), label: kind === 'asn' ? 'A Very Long Organisation Name Hosting Ltd' : undefined }));
    client.recorded[12]!.known_source_candidates = [
      ...many('country', 40, (i) => `C${i}`),
      ...many('asn', 12, (i) => `${64500 + i}`),
      ...many('client', 15, (i) => `some-client-library/${i}`),
      ...many('cidr', 12, (i) => `203.0.${i}.0/24`),
    ];
    client.thisWasMe = async () => {
      throw new ApiError('internal', `database is locked. ${'It is a long explanation. '.repeat(8)}`, 500);
    };
    const m = mount(client);
    await opened(m);
    await m.press('m', ' ', 'j', ' ', 'j', ' ', 'j', ' ', 'j', ' ', ENTER);
    let rows = m.screen().split('\n');
    expect(rows.length).toBeLessThanOrEqual(24);
    expect(m.screen()).toContain('This was you? Incident #12');
    expect(m.screen()).toContain('more');
    expect(m.screen()).toContain('--install-wide');
    expect(m.screen()).toContain('database is locked');
    expect(m.screen()).toContain('Esc cancel');
    expect(rows.some((l) => l.includes('╰'))).toBe(true);
    for (const [k, title] of [['d', 'Disable this key?'], ['o', 'Rotate '], ['x', 'Revoke '], ['R', 'Resolve incident #12']] as const) {
      await m.press(ESC, k);
      rows = m.screen().split('\n');
      expect(rows.length, k).toBeLessThanOrEqual(24);
      expect(m.screen(), k).toContain(title);
      expect(m.screen(), k).toContain('The same in a shell:');
      expect(rows.some((l) => l.includes('╭')), k).toBe(true);
      expect(rows.some((l) => l.includes('╰')), k).toBe(true);
    }
    m.unmount();
  });

  it('names an action that a resolved or acknowledged incident does not offer', async () => {
    const client = leaked();
    client.incidents[1]!.state = 'acknowledged';
    const m = mount(client);
    await loaded(m);
    // Acknowledged, it ranks under the open one.
    await m.press('j', ENTER, 'a');
    expect(m.screen()).toContain('Incident #13 is already acknowledged.');
    await m.press('R', 'c', 'y', 'm');
    expect(m.screen()).toContain('Incident #13 is resolved: only reset is left.');
    m.unmount();
  });
});

describe('safety', () => {
  it('takes no Ctrl or Alt chord for an action, a choice, a tick or a yes', async () => {
    const m = mount();
    await opened(m);
    const ctrl = (c: string) => String.fromCharCode(c.charCodeAt(0) - 96);
    await m.press(ctrl('a'), ctrl('d'), ctrl('x'), '\u001ba', '\u001bd');
    expect(m.screen()).toContain('WHAT FIRED');
    expect(m.screen()).not.toContain('Disable this key?');
    await m.press('d', ctrl('y'), '\u001by');
    expect(m.screen()).toContain('Disable this key?');
    await m.press(ESC, 'R', ctrl('f'), ctrl('c'));
    expect(m.screen()).toContain('Was it real?');
    await m.press('c', ctrl('y'));
    expect(m.screen()).toContain('Resolve it as confirmed?');
    await m.press(ESC, 'm', '\u001b ');
    expect(m.screen()).toContain('[ ] country');
    expect(changes(m.client)).toEqual([]);
    m.unmount();
  });

  it('shows the new boxes at once when what it saw changed as it was sent', async () => {
    const client = leaked();
    const m = mount(client);
    await opened(m);
    await m.press('m', ' ');
    client.recorded[12]!.known_source_candidates!.push({ kind: 'country', value: 'RU' });
    // No poll comes: what the check fetched is what is drawn.
    client.listKeys = () => new Promise(() => {});
    await m.press(ENTER);
    expect(m.screen()).toContain('NL, RU');
    expect(m.screen()).toContain('[ ] country');
    expect(changes(client)).toEqual([]);
    m.unmount();
  });

  it('does not take Alt+Enter for Enter on a typed name', async () => {
    const m = mount();
    await opened(m);
    await m.press('x', 'prod-api', '\u001b\r');
    expect(changes(m.client)).toEqual([]);
    expect(m.screen()).toContain('Revoke prod-api?');
    m.unmount();
  });

  it('says an incident expired, and puts "usually" before a client named like one', async () => {
    const client = leaked();
    client.recorded[12] = { detections: [detection(1, 'new_client', { client: 'curl/8', baseline: 'usual-app/1 (1 request)' })] };
    const m = mount(client, WIDE, 0, 100);
    await opened(m);
    expect(m.screen()).toContain('usually usual-app/1');
    Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'expired', resolved_by: 'system:detect' });
    await until(() => m.screen().includes('Incident #12 expired'), 'the news');
    expect(m.screen()).not.toContain('was expired');
    m.unmount();
  });

  it('closes the enable dialog when someone else resolves the incident', async () => {
    const client = leaked();
    client.keys.at(-1)!.status = 'disabled';
    const m = mount(client, WIDE, 0, 100);
    await opened(m);
    await m.press('e');
    expect(m.screen()).toContain('Enable this key?');
    Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'confirmed', resolved_by: 'alice' });
    await until(() => m.screen().includes('resolved by alice'), 'the news');
    await m.settle();
    expect(m.screen()).not.toContain('Enable this key?');
    await m.press('y');
    expect(changes(client)).toEqual([]);
    m.unmount();
  });

  it('does not enable a key for an incident resolved before the screen heard of it', async () => {
    const client = leaked();
    client.keys.at(-1)!.status = 'disabled';
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('e');
    Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'confirmed', resolved_by: 'alice' });
    await m.press('y');
    expect(changes(client)).toEqual([]);
    expect(m.screen()).toContain('was resolved by alice as confirmed.');
    m.unmount();
  });

  it('says nothing of a lost secret when a rotation is refused', async () => {
    const client = leaked();
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('o', 'prod-api');
    Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'false_positive', resolved_by: 'alice' });
    await m.press(ENTER);
    const said = m.screen().replace(/\s+/g, ' ');
    expect(said).toContain('was resolved by alice as false positive. Could not rotate');
    expect(said).not.toContain('was rotated');
    m.unmount();
  });

  it('warns of a lost secret when a rotation answers with something unreadable', async () => {
    const client = leaked();
    client.rotateKey = async () => {
      throw new ApiError('internal', 'the admin API sent a reply that is not JSON', 200);
    };
    const m = mount(client);
    await opened(m);
    await m.press('o', 'prod-api', ENTER);
    expect(m.screen().replace(/[│\s]+/g, ' ')).toContain('If prod-api was rotated');
    m.unmount();
  });

  it('ignores Enter for a moment when the boxes of this was me change under it', async () => {
    const client = leaked();
    const m = mount(client, WIDE, 600, 100);
    await opened(m);
    await tick(650);
    await m.press('m');
    await tick(650);
    await m.press(' ');
    client.recorded[12]!.known_source_candidates!.push({ kind: 'country', value: 'RU' });
    await until(() => m.screen().includes('What it saw has changed'), 'the change');
    await m.press(ENTER, ' ');
    expect(changes(client)).toEqual([]);
    expect(m.screen()).toContain('[ ] country');
    await tick(650);
    await m.press(ENTER);
    expect(changes(client)).toEqual(['this-was-me 12 {"add_known_sources":false}']);
    m.unmount();
  });

  it('does not say "usually" twice of what kman words as a sentence', async () => {
    const client = leaked();
    client.recorded[12] = {
      detections: [
        detection(1, 'new_network', { address_withheld: true, baseline: '2 usual networks' }),
        detection(2, 'new_country', { country: 'DE', baseline: 'no usual places recorded' }),
      ],
    };
    const m = mount(client, WIDE);
    await opened(m);
    expect(m.screen()).toContain('2 usual networks');
    expect(m.screen()).toContain('no usual places recorded');
    expect(m.screen()).not.toContain('usually 2');
    expect(m.screen()).not.toContain('usually no');
    m.unmount();
  });

  it('asks for fresh data once after a change made for an incident', async () => {
    const client = leaked();
    let polls = 0;
    const list = client.listKeys.bind(client);
    client.listKeys = async () => (polls++, list());
    const m = mount(client);
    await opened(m);
    for (const keys of [['d', 'y'], ['x', 'prod-api', ENTER]]) {
      const before = polls;
      await m.press(...keys);
      expect(polls - before, keys.join()).toBe(1);
    }
    m.unmount();
  });

  it('ignores action keys just after the view appears, and just after a dialog does', async () => {
    const m = mount(leaked(), SMALL, 600);
    await opened(m);
    await m.press('a', 'x');
    expect(changes(m.client)).toEqual([]);
    expect(m.screen()).not.toContain('Revoke prod-api?');
    await tick(650);
    await m.press('d');
    expect(m.screen()).toContain('Disable this key?');
    await m.press('y');
    expect(changes(m.client)).toEqual([]);
    expect(m.screen()).toContain('Disable this key?');
    await tick(650);
    await m.press('y');
    expect(changes(m.client)).toEqual(['disable k_prod 12']);
    m.unmount();
  });

  it('takes keys though the wall clock is stepped back', async () => {
    const m = mount();
    await opened(m);
    // As a time sync does, to before the view appeared.
    const wall = vi.spyOn(Date, 'now').mockReturnValue(Date.now() - 60_000);
    await m.press('d');
    expect(m.screen()).toContain('Disable this key?');
    await m.press('y');
    wall.mockRestore();
    expect(changes(m.client)).toEqual(['disable k_prod 12']);
    m.unmount();
  });

  it('ignores a choice, a y, a tick and Enter in a dialog just shown, but not Esc', async () => {
    const m = mount(leaked(), SMALL, 600);
    await opened(m);
    await tick(650);
    await m.press('R', 'c');
    expect(m.screen()).toContain('Was it real?');
    await tick(650);
    await m.press('c', 'y');
    expect(m.screen()).toContain('Resolve it as confirmed?');
    await m.press(ESC);
    await tick(650);
    await m.press('m', ' ', ENTER);
    expect(m.screen()).toContain('[ ] country');
    await m.press(ESC);
    await tick(650);
    await m.press('x', 'prod-api', ENTER);
    expect(m.screen()).toContain('Revoke prod-api?');
    expect(changes(m.client)).toEqual([]);
    m.unmount();
  });

  it('turns actions off while kman is unreachable, and the footer says why', async () => {
    const client = leaked();
    const m = mount(client, SMALL, 0, 100);
    await opened(m);
    client.fail = new ApiError('unreachable', 'cannot reach it: connection refused');
    await until(() => m.screen().includes('Actions off:'), 'offline');
    await m.settle();
    expect(m.screen()).toContain('kman is unreachable');
    expect(m.screen()).toContain('Actions are off while kman does not answer.');
    expect(m.screen()).toContain('WHAT FIRED');
    for (const k of ['a', 'm', 'R', 'd', 'o', 'x']) await m.press(k);
    expect(m.screen()).toContain('WHAT FIRED');
    expect(changes(client)).toEqual([]);
    client.fail = undefined;
    await until(() => !m.screen().includes('Actions off:'), 'online again', 4000);
    await m.press('a');
    expect(changes(client)).toEqual(['acknowledge 12']);
    m.unmount();
  });

  it('sends nothing from a dialog left open when kman stops answering', async () => {
    const client = leaked();
    const m = mount(client, SMALL, 0, 100);
    await opened(m);
    await m.press('x', 'prod-api');
    client.fail = new ApiError('unreachable', 'cannot reach it: connection refused');
    await until(() => m.screen().includes('offline'), 'offline');
    await m.press(ENTER);
    expect(m.screen()).toContain('nothing was sent');
    expect(changes(client)).toEqual([]);
    m.unmount();
  });

  it('says who resolved it when someone else does, closes the dialog and keeps the view', async () => {
    const client = leaked();
    const m = mount(client, WIDE, 0, 100);
    await opened(m);
    await m.press('x', 'prod');
    Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'false_positive', resolved_by: 'alice', resolved_at: new Date(NOW).toISOString() });
    await until(() => m.screen().includes('resolved by alice'), 'the news');
    await m.settle();
    expect(m.screen()).not.toContain('Revoke prod-api?');
    expect(m.screen()).toContain('Incident #12 was resolved by alice as false positive.');
    expect(m.screen()).toContain('Resolved by alice as false positive');
    expect(m.screen()).toContain('Incident #12 · prod-api');
    expect(changes(client)).toEqual([]);
    m.unmount();
  });

  it('says who resolved it when an action arrives too late', async () => {
    const client = leaked();
    const m = mount(client, WIDE);
    await opened(m);
    Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'confirmed', resolved_by: 'alice', resolved_at: new Date(NOW).toISOString() });
    await m.press('a');
    expect(m.screen()).toContain('already resolved');
    expect(m.screen()).toContain('was resolved by alice as confirmed.');
    expect(m.screen()).toContain('Resolved by alice as confirmed');
    m.unmount();
  });

  it('does not call this was me done on an incident someone else resolved', async () => {
    const client = leaked();
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('m', ' ');
    Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'false_positive', resolved_by: 'alice' });
    await m.press(ENTER);
    expect(changes(client)).toEqual([]);
    expect(m.screen()).toContain('was resolved by alice');
    expect(m.screen()).not.toContain('This was you?');
    m.unmount();
  });

  it('keeps a dialog open with the reason when a change fails', async () => {
    const client = leaked();
    client.rotateKey = async () => {
      throw new ApiError('internal', 'database is locked', 500);
    };
    const m = mount(client);
    await opened(m);
    await m.press('o', 'prod-api', ENTER);
    expect(m.screen()).toContain('Rotate prod-api?');
    expect(m.screen()).toContain('database is locked');
    m.unmount();
  });

  it('warns that the new secret is lost when a rotation happened but its answer did not arrive', async () => {
    const client = leaked();
    const rotate = client.rotateKey.bind(client);
    client.rotateKey = async (id, n) => {
      await rotate(id, n);
      throw new ApiError('unreachable', 'cannot reach it: timed out');
    };
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('o', 'prod-api', ENTER);
    // The warning wraps in the footer.
    const said = m.screen().replace(/\s+/g, ' ');
    expect(said).toContain('timed out');
    expect(said).toContain('prod-api was rotated by owner, but no new secret arrived here');
    expect(said).toContain('run kman key rotate prod-api for one you can see');
    expect(said).not.toContain('Could not rotate');
    expect(m.screen()).toContain('Resolved by owner as confirmed');
    m.unmount();
  });

  it('never says to rotate again after someone else’s rotation, whatever this one got', async () => {
    for (const failure of [
      new ApiError('conflict', 'incident 12 is already resolved (confirmed)', 409),
      new ApiError('internal', 'the admin API answered 502', 502),
      new ApiError('unreachable', 'cannot reach it: timed out'),
    ]) {
      const client = leaked();
      const rotate = client.rotateKey.bind(client);
      client.rotateKey = async (id, n) => {
        await rotate(id, n);
        Object.assign(client.incidents[0]!, { resolved_by: 'alice' });
        for (const x of client.recorded[12]!.actions!) x.actor = 'alice';
        throw failure;
      };
      const m = mount(client, WIDE);
      await opened(m);
      await m.press('o', 'prod-api', ENTER);
      const said = m.screen().replace(/\s+/g, ' ');
      expect(said, failure.message).toContain('prod-api was rotated by alice, and the new secret did not come here');
      expect(said, failure.message).not.toContain('kman key rotate prod-api');
      m.unmount();
    }
  });

  it('advises no rotation over one recorded while who this is is unknown', async () => {
    const client = leaked();
    client.whoami = async () => {
      throw new ApiError('internal', 'database is locked', 500);
    };
    const rotate = client.rotateKey.bind(client);
    client.rotateKey = async (id, n) => {
      await rotate(id, n);
      for (const x of client.recorded[12]!.actions!) x.actor = 'alice';
      throw new ApiError('internal', 'the admin API answered 502', 502);
    };
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('o', 'prod-api', ENTER);
    const said = m.screen().replace(/\s+/g, ' ');
    expect(said).toContain('prod-api was rotated by alice, but no new secret arrived here: check with alice before rotating it again');
    expect(said).not.toContain('kman key rotate prod-api');
    m.unmount();
  });

  it('names who is recorded as resolving it while who this is is unknown', async () => {
    const client = leaked();
    client.whoami = async () => {
      throw new ApiError('internal', 'database is locked', 500);
    };
    const wasMe = client.thisWasMe.bind(client);
    client.thisWasMe = async (id, input) => {
      const got = await wasMe(id, input);
      return { ...got, incident: { ...got.incident, resolved_by: 'alice' } };
    };
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('m', ' ', ENTER);
    const said = m.screen().replace(/\s+/g, ' ');
    expect(said).toContain('#12 resolved: false positive, by alice. Added 1 known source.');
    m.unmount();
  });

  it('names it in the resolve notice too while who this is is unknown', async () => {
    const client = leaked();
    client.whoami = async () => {
      throw new ApiError('internal', 'database is locked', 500);
    };
    const resolve = client.resolveIncident.bind(client);
    client.resolveIncident = async (id, as) => ({ ...(await resolve(id, as)), resolved_by: 'alice' });
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('R', 'c', 'y');
    expect(m.screen().replace(/\s+/g, ' ')).toContain('Incident #12 is resolved as confirmed, by alice.');
    m.unmount();
  });

  it('shows an incident whose read takes longer than the polls', async () => {
    const client = leaked();
    const read = client.incident.bind(client);
    client.incident = async (id) => {
      await tick(320);
      return read(id);
    };
    const m = mount(client, WIDE, 0, 100);
    await loaded(m);
    await m.press('j', ENTER);
    await until(() => m.screen().includes('WHAT FIRED'), 'the incident');
    m.unmount();
  });

  it('shows it resolved once that is read, though no poll follows and an older read comes after', async () => {
    const client = leaked();
    const read = client.incident.bind(client);
    const m = mount(client, WIDE, 0, 100);
    await opened(m);
    // A read from before it was resolved, answered only at the end.
    const before = await read(12);
    let release: (() => void) | undefined;
    client.incident = async () => new Promise((r) => (release = () => r(before)));
    await until(() => release !== undefined, 'the slow read');
    client.incident = read;
    const acknowledge = client.acknowledgeIncident.bind(client);
    client.acknowledgeIncident = async (id) => {
      Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'confirmed', resolved_by: 'alice', resolved_at: new Date(NOW).toISOString() });
      client.listKeys = async () => {
        throw new ApiError('internal', 'database is locked', 500);
      };
      return acknowledge(id);
    };
    await m.press('a');
    expect(m.screen()).toContain('Resolved by alice as confirmed');
    release!();
    await m.settle();
    expect(m.screen()).toContain('Resolved by alice as confirmed');
    expect(m.screen()).not.toContain('HIGH · OPEN');
    m.unmount();
  });

  it('drops a read begun before a change, and reads again at the next poll', async () => {
    const client = leaked();
    const read = client.incident.bind(client);
    const m = mount(client, WIDE, 0, 100);
    await opened(m);
    const before = await read(12);
    let release: (() => void) | undefined;
    client.incident = async () => new Promise((r) => (release = () => r(before)));
    await until(() => release !== undefined, 'the slow read');
    client.incident = read;
    await m.press('a');
    await until(() => m.screen().includes('HIGH · ACKNOWLEDGED'), 'the change drawn');
    // No later read answers, so only the old one could redraw it.
    client.incident = () => new Promise(() => {});
    release!();
    await m.settle();
    expect(m.screen()).toContain('HIGH · ACKNOWLEDGED');
    m.unmount();
  });

  it('takes a proxy’s 502 after a rotation for a rotation, not a failure', async () => {
    const client = leaked();
    const rotate = client.rotateKey.bind(client);
    client.rotateKey = async (id, n) => {
      await rotate(id, n);
      throw new ApiError('internal', 'the admin API answered 502', 502);
    };
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('o', 'prod-api', ENTER);
    const said = m.screen().replace(/\s+/g, ' ');
    expect(said).toContain('prod-api was rotated by owner, but no new secret arrived here');
    expect(said).toContain('run kman key rotate prod-api for one you can see');
    expect(said).not.toContain('Could not rotate');
    m.unmount();
  });

  it('warns that a key may have been rotated while a 504 leaves the incident open, until it is resolved without one', async () => {
    const client = leaked();
    client.rotateKey = async () => {
      throw new ApiError('internal', 'the admin API answered 504', 504);
    };
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('o', 'prod-api', ENTER);
    expect(m.screen().replace(/[│\s]+/g, ' ')).toContain('If prod-api was rotated');
    client.rotateKey = async () => {
      Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'false_positive', resolved_by: 'alice' });
      throw new ApiError('conflict', 'incident 12 is already resolved', 409);
    };
    await m.press(ENTER);
    const said = m.screen().replace(/\s+/g, ' ');
    // Resolved with no rotation recorded: neither try rotated anything.
    expect(said).toContain('was resolved by alice as false positive. Could not rotate: incident 12 is already resolved');
    expect(said).not.toContain('was rotated');
    m.unmount();
  });

  it('says it was rotated when the poll is what finds the incident resolved after a lost answer', async () => {
    const client = leaked();
    const rotate = client.rotateKey.bind(client);
    const detail = client.incident.bind(client);
    client.rotateKey = async (id, n) => {
      await rotate(id, n);
      let failed = false;
      // The look straight after fails too.
      client.incident = async (x) => {
        if (failed) return detail(x);
        failed = true;
        throw new ApiError('unreachable', 'cannot reach it: timed out');
      };
      throw new ApiError('unreachable', 'cannot reach it: timed out');
    };
    const m = mount(client, WIDE, 0, 100);
    await opened(m);
    await m.press('o', 'prod-api', ENTER);
    await until(() => m.screen().replace(/\s+/g, ' ').includes('prod-api was rotated by owner, but no new secret'), 'the news');
    m.unmount();
  });

  it('says who got there first when it is marked a false positive just before this was me', async () => {
    const client = leaked();
    client.thisWasMe = async () => {
      Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'false_positive', resolved_by: 'alice' });
      return { incident: structuredClone(client.incidents[0]!), added: [{ id: 1, kind: 'country', value: 'NL', label: 'this was me' }] };
    };
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('m', ' ', ENTER);
    expect(m.screen().replace(/\s+/g, ' ')).toContain('Incident #12 was already resolved by alice as false positive. Added 1 known source.');
    m.unmount();
  });

  it('says a canary’s use was refused, and where it was planted', async () => {
    const m = mount(leaked(), WIDE);
    await opened(m, 13);
    expect(m.screen()).toContain('planted: the .env of the wiki');
    expect(m.screen()).toMatch(/refused/);
    m.unmount();
  });

  it('says nothing was rotated when someone else resolved it and no rotation is recorded', async () => {
    const client = leaked();
    client.rotateKey = async () => {
      Object.assign(client.incidents[0]!, { state: 'resolved', resolution: 'false_positive', resolved_by: 'alice' });
      throw new ApiError('unreachable', 'cannot reach it: timed out');
    };
    const m = mount(client, WIDE);
    await opened(m);
    await m.press('o', 'prod-api', ENTER);
    const said = m.screen().replace(/\s+/g, ' ');
    expect(said).toContain('was resolved by alice as false positive. Could not rotate: cannot reach it: timed out');
    expect(said).not.toContain('was rotated');
    m.unmount();
  });
});
