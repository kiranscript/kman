// Which requests are flagged, what the flags must not claim, and how each poll's pages are
// joined to the rows held.
import { describe, expect, it } from 'vitest';
import type { Detection, EventPage, IncidentDetail } from '../src/api.js';
import { flagOf, matches, MAX_ROWS, mergeNewest, mergeOlder, resultText } from '../src/requests.js';
import type { FlagSources } from '../src/requests.js';
import { detection, incident, NOW, request } from './fakes.js';

const open = incident({ id: 12, key_id: 'k_prod', opened_at: new Date(NOW - 40 * 60_000).toISOString() });
const seen = { country: 'NL', asn: 60729, client: 'curl/8' };
/** A request that looks like what incident #12 saw, a minute ago. */
const alike = (over: Parameters<typeof request>[0] = { id: 1 }, ago?: number) => request({ key_id: 'k_prod', key_name: 'prod-api', ...seen, ...over }, ago);

const sources = (detections: Detection[] = [detection(1, 'new_country', seen)], over: Partial<FlagSources> = {}): FlagSources => ({
  canaries: new Set(['k_wiki']),
  incidents: [open],
  details: new Map<number, IncidentDetail>([[12, { ...open, detections, actions: [], known_source_candidates: [] }]]),
  ...over,
});

describe('flagOf', () => {
  it('flags a request like what an open incident saw, and says which rules', () => {
    expect(flagOf(alike(), sources())).toEqual({ severity: 'danger', why: 'like incident #12: new country', incident: 12 });
  });

  it('flags the operator’s own request when it looks the same: events cannot tell them apart', () => {
    expect(flagOf(alike({ id: 2, kind: 'forwarded', status: 200 }), sources())?.severity).toBe('danger');
  });

  it('flags one from the moment the incident opened, and not one from before it', () => {
    expect(flagOf(alike({ id: 1 }, 40 * 60_000), sources())?.incident).toBe(12);
    expect(flagOf(alike({ id: 1 }, 40 * 60_000 + 1), sources())).toBeUndefined();
  });

  it('needs every one of country, ASN and client the detection recorded', () => {
    for (const other of [{ country: 'US' }, { asn: 16509 }, { client: 'openai-python/1' }, { country: undefined }, { asn: undefined }, { client: undefined }]) {
      expect(flagOf(alike({ id: 1, ...other }), sources()), JSON.stringify(other)).toBeUndefined();
    }
    // What it did not record is not asked for.
    expect(flagOf(alike({ id: 1, asn: 1 }), sources([detection(1, 'new_country', { country: 'NL' })]))?.incident).toBe(12);
  });

  it('counts a detection that recorded only a client for a new client alone', () => {
    const only = { client: 'curl/8' };
    expect(matches(detection(1, 'new_client', only), alike())).toBe(true);
    for (const rule of ['new_country', 'new_asn', 'new_network', 'spike']) expect(matches(detection(1, rule, only), alike()), rule).toBe(false);
  });

  it('never matches a detection that lacks what was new, or records nothing', () => {
    expect(matches(detection(1, 'new_country', { asn: 60729 }), alike())).toBe(false);
    expect(matches(detection(1, 'new_asn', { country: 'NL' }), alike())).toBe(false);
    expect(matches(detection(1, 'new_client', { country: 'NL' }), alike())).toBe(false);
    expect(matches(detection(1, 'spike', {}), alike())).toBe(false);
    expect(matches(detection(1, 'spike', { observed: '90/min' }), alike())).toBe(false);
  });

  it('matches a new network only with the address in it, which takes expose_client_addresses', () => {
    const net = detection(1, 'new_network', { ...seen, network: '203.0.113.0/24' });
    expect(matches(net, alike({ id: 1, client_address: '203.0.113.77' }))).toBe(true);
    expect(matches(net, alike({ id: 1, client_address: '::ffff:203.0.113.77' }))).toBe(true);
    expect(matches(net, alike({ id: 1, client_address: '203.0.114.77' }))).toBe(false);
    // Off: the event has no address, and the evidence no network.
    expect(matches(net, alike({ id: 1 }))).toBe(false);
    expect(matches(detection(1, 'new_network', { ...seen, address_withheld: true }), alike({ id: 1, client_address: '203.0.113.77' }))).toBe(false);
    const six = detection(1, 'new_network', { network: '2001:db8:1:2::/64' });
    expect(matches(six, alike({ id: 1, client_address: '2001:db8:1:2:ffff::1' }))).toBe(true);
    expect(matches(six, alike({ id: 1, client_address: '2001:db8:1:3::1' }))).toBe(false);
    expect(matches(detection(1, 'new_network', { network: 'nonsense' }), alike({ id: 1, client_address: '203.0.113.77' }))).toBe(false);
  });

  it('drops an incident’s flags once it is resolved, and keeps to its own key', () => {
    expect(flagOf(alike(), sources(undefined, { incidents: [{ ...open, state: 'resolved' }] }))).toBeUndefined();
    expect(flagOf(alike({ id: 1, key_id: 'k_other' }), sources())).toBeUndefined();
    expect(flagOf(alike(), sources(undefined, { incidents: [{ ...open, state: 'acknowledged' }] }))?.incident).toBe(12);
  });

  it('flags nothing for an incident whose detail has not loaded', () => {
    expect(flagOf(alike(), sources(undefined, { details: new Map() }))).toBeUndefined();
  });

  it('flags every use of a canary key, with its incident when it has one', () => {
    const use = request({ id: 1, key_id: 'k_wiki', kind: 'refused', status: 401, refusal_code: 'invalid_key' }, 90 * 86_400_000);
    expect(flagOf(use, sources())).toEqual({ severity: 'danger', why: 'used a canary key', incident: undefined, canary: true });
    const canary = incident({ id: 13, key_id: 'k_wiki', rules: ['canary'] });
    expect(flagOf(use, sources(undefined, { incidents: [canary] }))).toMatchObject({ severity: 'danger', incident: 13, why: 'used a canary key: incident #13' });
    // The incident says it is a canary when the canaries could not be listed.
    expect(flagOf(use, sources(undefined, { canaries: undefined, incidents: [canary] }))?.canary).toBe(true);
  });

  it('warns of a refusal, and of a request with no key kman knows', () => {
    expect(flagOf(request({ id: 1, kind: 'refused', status: 429, refusal_code: 'rate_limited' }), sources())).toEqual({ severity: 'warn', why: 'refused: rate_limited' });
    const nobody = request({ id: 1, kind: 'refused', unidentified_key: true, key_id: undefined, refusal_code: 'invalid_key' });
    expect(flagOf(nobody, sources())).toEqual({ severity: 'warn', why: 'no key kman knows: invalid_key' });
    expect(flagOf(request({ id: 1 }), sources())).toBeUndefined();
  });

  it('puts the incident and why in the result', () => {
    const e = alike({ id: 1, kind: 'refused', status: 403, refusal_code: 'address_not_allowed' });
    expect(resultText(e, flagOf(e, sources()))).toBe('403 #12 address_not_allowed');
    expect(resultText(request({ id: 1 }), undefined)).toBe('200');
    const use = request({ id: 1, key_id: 'k_wiki', kind: 'refused', status: 401, refusal_code: 'invalid_key' });
    expect(resultText(use, flagOf(use, sources(undefined, { incidents: [incident({ id: 13, key_id: 'k_wiki', rules: ['canary'] })] })))).toBe('401 #13 canary');
  });
});

describe('joining the pages to the rows held', () => {
  const rows = (...ids: number[]) => ids.map((id) => request({ id }));
  const page = (ids: number[], more = false, over: Partial<EventPage> = {}): EventPage => ({ events: rows(...ids), more, next_before: more ? ids.at(-1) : undefined, ...over });
  const ids = (f: { rows: { id: number }[] }) => f.rows.map((r) => r.id);

  it('adds what is new above, keeping the older rows and whether there are more', () => {
    const got = mergeNewest({ rows: rows(3, 2, 1), more: false }, [page([5, 4, 3], true)]);
    expect(ids(got)).toEqual([5, 4, 3, 2, 1]);
    expect(got.more).toBe(false);
  });

  it('starts over rather than leave a gap', () => {
    const got = mergeNewest({ rows: rows(3, 2, 1), more: false }, [page([30, 29], true), page([28, 27], true)]);
    expect(ids(got)).toEqual([30, 29, 28, 27]);
    expect(got.more).toBe(true);
  });

  it('takes a page with nothing older as all there is', () => {
    expect(ids(mergeNewest({ rows: rows(3, 2, 1), more: true }, [page([4, 3])]))).toEqual([4, 3]);
    expect(ids(mergeNewest({ rows: rows(3), more: false }, [page([])]))).toEqual([]);
  });

  it('takes what was just fetched over what is held, keeping the rows that say the same', () => {
    const held = { rows: [request({ id: 2, key_name: 'old' }), request({ id: 1 })], more: true };
    const fresh = page([2, 1], true);
    fresh.events = [request({ id: 2, key_name: 'new', client_address: '203.0.113.7' }), structuredClone(held.rows[1]!)];
    const got = mergeNewest(held, [fresh]);
    expect(got.rows[0]).toMatchObject({ key_name: 'new', client_address: '203.0.113.7' });
    expect(got.rows[1]).toBe(held.rows[1]);
    // Nothing new: the very same rows, so nothing is worked out again.
    const again = page([2, 1], true);
    again.events = structuredClone(got.rows);
    expect(mergeNewest(got, [again]).rows).toBe(got.rows);
    again.client_addresses_withheld = true;
    again.events = [structuredClone(held.rows[1]!)];
    expect(mergeNewest({ rows: [held.rows[1]!], more: true }, [again]).rows[0]).toBe(held.rows[1]);
  });

  it('forgets addresses once they are withheld', () => {
    const held = { rows: [request({ id: 1, client_address: '203.0.113.7' })], more: false };
    const got = mergeNewest(held, [page([2, 1], true, { client_addresses_withheld: true })]);
    expect(got.rows.every((r) => r.client_address === undefined)).toBe(true);
    expect(mergeOlder({ rows: held.rows, more: true }, page([0], false, { client_addresses_withheld: true }), 1).rows[0]!.client_address).toBeUndefined();
  });

  it('adds an older page only below the row it was asked after', () => {
    expect(ids(mergeOlder({ rows: rows(5, 4), more: true }, page([3, 2]), 4))).toEqual([5, 4, 3, 2]);
    expect(ids(mergeOlder({ rows: rows(30, 29), more: true }, page([3, 2]), 4))).toEqual([30, 29]);
  });

  it('keeps the selected request past the most it holds, up to twice as many', () => {
    const newest = 2 * MAX_ROWS + 10;
    const full = { rows: rows(...Array.from({ length: MAX_ROWS }, (_, i) => MAX_ROWS - i)), more: true };
    const next = page([MAX_ROWS + 2, MAX_ROWS + 1, MAX_ROWS], true);
    // The oldest are let go, but not the selected one.
    expect(mergeNewest(full, [next]).rows.at(-1)!.id).toBe(3);
    const kept = mergeNewest(full, [next], 1);
    expect(kept.rows.at(-1)!.id).toBe(1);
    expect(kept.rows).toHaveLength(MAX_ROWS + 2);
    const huge = { rows: rows(...Array.from({ length: newest }, (_, i) => newest - i)), more: true };
    expect(mergeNewest(huge, [page([newest], true)], 1).rows).toHaveLength(2 * MAX_ROWS);
  });

  it('reads an address and a network however they are written', () => {
    const cases: [string, string, boolean][] = [
      ['203.0.113.0/24', '203.0.113.255', true],
      ['203.0.113.0/25', '203.0.113.127', true],
      ['203.0.113.0/25', '203.0.113.128', false],
      ['203.0.112.0/23', '203.0.113.9', true],
      ['203.0.113.0/24', '::FFFF:CB00:7109', true],
      ['203.0.113.0/24', '2001:db8::1', false],
      ['2001:db8:1:2::/64', '2001:0DB8:0001:0002:0000:0000:0000:0001', true],
      ['2001:db8::/64', '2001:db8::192.0.2.1', true],
      ['2001:db8::/64', '2001:db9::1', false],
      ['fe80::/64', 'fe80::1%eth0', true],
      ['::/0', '2001:db8::1', true],
      ['2001:db8:1:2::/64', '203.0.113.7', false],
      ['203.0.113.0/24', 'unix', false],
    ];
    for (const [network, address, want] of cases) {
      expect(matches(detection(1, 'new_network', { network }), alike({ id: 1, client_address: address })), `${address} in ${network}`).toBe(want);
    }
  });

  it('flags every row held against a full incident in well under a poll', () => {
    const many = Array.from({ length: 100 }, (_, n) => detection(n, 'new_network', { ...seen, network: `10.${n}.0.0/24` }));
    const all = sources(many);
    // Like in everything but the network, so nothing is ruled out before it is compared.
    const rows = Array.from({ length: MAX_ROWS }, (_, n) => alike({ id: n, client_address: `203.0.${n % 250}.${n % 200}` }));
    const start = performance.now();
    expect(rows.filter((e) => flagOf(e, all)).length).toBe(0);
    expect(performance.now() - start).toBeLessThan(1000);
  });

  it('does not take an address in a network that is not one', () => {
    for (const network of ['203.0.113.0/99', '2001:db8::/200', '203.0.113.0/24/8', '203.0.113.0']) {
      expect(matches(detection(1, 'new_network', { network }), alike({ id: 1, client_address: '203.0.113.7' })), network).toBe(false);
    }
  });

  it('holds only so many rows', () => {
    const many = Array.from({ length: MAX_ROWS + 5 }, (_, i) => MAX_ROWS + 5 - i);
    const got = mergeNewest({ rows: [], more: false }, [page(many)]);
    expect(got.rows).toHaveLength(MAX_ROWS);
    expect(got.more).toBe(true);
  });
});
